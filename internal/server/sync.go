package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// ---- the device endpoint: POST /api/v1/device/sync ----
//
// The contract is docs/protocol.md ("NTP Clock: server protocol, version 1"):
// the device opens every connection, sends its status and settings and gets
// back what to change and what to do. Bodies are at most 8 KiB both ways, no
// redirects, unknown fields are ignored.

const maxDeviceBody = 8 << 10

// syncReq is the part of a report the server reads; the rest is kept as status.
type syncReq struct {
	DeviceID       string                `json:"device_id"`
	ConfigRev      int64                 `json:"config_rev"`
	Config         map[string]string     `json:"config"`
	ConfigErrors   map[string]string     `json:"config_errors"`
	CommandResults []store.CommandResult `json:"command_results"`
	Events         []map[string]any      `json:"events"`
}

// statusDrop are the request fields that are not status.
var statusDrop = []string{"device_id", "config", "config_rev", "config_errors", "command_results", "events"}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleDeviceSync(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := s.limits.failWait(ip); wait > 0 {
		tooMany(w, wait)
		return
	}
	if r.ContentLength > maxDeviceBody {
		writeErr(w, http.StatusRequestEntityTooLarge, "at most 8 KiB")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeviceBody))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "at most 8 KiB")
		return
	}
	var req syncReq
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	tok := bearer(r)
	if tok == "" {
		s.limits.fail(ip)
		writeErr(w, http.StatusUnauthorized, "missing token")
		return
	}
	hwid := strings.ToUpper(strings.TrimSpace(req.DeviceID))
	d, err := s.store.DeviceByHWID(r.Context(), hwid)
	if errors.Is(err, store.ErrNotFound) || hwid == "" {
		s.limits.fail(ip)
		writeErr(w, http.StatusNotFound, "unknown device")
		return
	} else if err != nil {
		s.fail(w, err, "device")
		return
	}
	if subtle.ConstantTimeCompare([]byte(store.HashToken(tok)), []byte(d.TokenHash)) != 1 {
		s.limits.fail(ip)
		s.log.Info("device: wrong token", "device", d.HWID, "ip", ip)
		writeErr(w, http.StatusUnauthorized, "wrong token")
		return
	}
	if wait := s.limits.deviceWait(d.ID); wait > 0 {
		tooMany(w, wait)
		return
	}
	out, err := s.syncDevice(r.Context(), d, raw, req, ip)
	if err != nil {
		s.log.Error("device sync", "device", d.HWID, "err", err)
		writeErr(w, http.StatusInternalServerError, "try again")
		return
	}
	b, _ := json.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeErr(w, http.StatusTooManyRequests, "slow down")
}

// syncDevice stores a report and builds the answer (the endpoint and the
// demo clocks both come through here).
func (s *Server) syncDevice(ctx context.Context, d *store.Device, raw []byte, req syncReq, ip string) (map[string]any, error) {
	k, ok := kinds.Get(d.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", d.Kind)
	}
	var all map[string]json.RawMessage
	_ = json.Unmarshal(raw, &all)
	for _, f := range statusDrop {
		delete(all, f)
	}
	status, _ := json.Marshal(all)
	in := store.SyncIn{Status: status, ConfigRev: req.ConfigRev, ConfigErrors: req.ConfigErrors, Results: req.CommandResults, IP: ip, Now: time.Now(), MaxCommands: 10}
	if req.Config != nil {
		in.Config = map[string]string{}
		for key, v := range req.Config {
			if len(in.Config) >= 120 {
				break
			}
			in.Config[key] = v
		}
	}
	// readings: the kind's chart readings are kept in 5-minute buckets
	var statusMap map[string]any
	_ = json.Unmarshal(status, &statusMap)
	reported := d.Reported
	if in.Config != nil {
		reported = in.Config
	}
	values := k.Values(statusMap, reported)
	in.Readings = map[string]float64{}
	for _, r := range k.Readings {
		if f, ok := values[r.Key].(float64); ok && r.Chart && !math.IsNaN(f) && !math.IsInf(f, 0) {
			in.Readings[r.Key] = f
		}
	}
	in.Events = deviceEvents(k, req.Events, in.Now)
	out, err := s.store.Sync(ctx, d, k, in)
	if err != nil {
		return nil, err
	}
	s.syncs.Add(1)
	resp := map[string]any{"config_rev": out.ConfigRev, "poll_s": out.PollS}
	if out.Config != nil {
		resp["config"] = out.Config
	}
	if len(out.Commands) > 0 {
		cmds := make([]map[string]any, 0, len(out.Commands))
		for _, c := range out.Commands {
			m := map[string]any{}
			_ = json.Unmarshal(c.Args, &m)
			m["id"] = fmt.Sprintf("c-%d", c.ID)
			m["name"] = c.Name
			cmds = append(cmds, m)
		}
		resp["commands"] = cmds
	}
	t := map[string]any{"unix": time.Now().Unix()}
	if out.TZ != "" {
		t["tz"] = out.TZ
	}
	resp["time"] = t
	raised, cleared := s.checkAlerts(ctx, d, k, values, reported)
	s.afterSync(d, k, out, raised, cleared, values)
	return resp, nil
}

// deviceEvents keeps the events of a kind it knows (at most 20 a request),
// with only the fields the kind declares and their allowed values.
func deviceEvents(k *kinds.Kind, raw []map[string]any, now time.Time) []store.EventIn {
	var out []store.EventIn
	for _, e := range raw {
		if len(out) >= 20 {
			break
		}
		typ, _ := e["type"].(string)
		def, ok := k.Event(typ)
		if !ok {
			continue
		}
		in := store.EventIn{Type: typ, Data: map[string]any{}}
		if id, ok := e["id"].(string); ok && len(id) <= 64 {
			in.ExtID = id
		}
		if at, ok := e["at"].(float64); ok && at > 1e9 {
			in.At = time.Unix(int64(at), 0)
		}
		for _, f := range def.Fields {
			v, ok := e[f.Name].(string)
			if !ok {
				continue
			}
			if len(f.Options) > 0 && !hasOpt(f.Options, v) {
				continue
			}
			if len(v) <= 64 {
				in.Data[f.Name] = v
			}
		}
		_ = now
		out = append(out, in)
	}
	return out
}

func hasOpt(opts []kinds.Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// checkAlerts compares the alerts that hold now with the ones stored, keeps
// the new set and logs every change.
func (s *Server) checkAlerts(ctx context.Context, d *store.Device, k *kinds.Kind, values map[string]any, settings map[string]string) (raised, cleared []string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	active := map[string]string{}
	for _, id := range k.ActiveAlerts(values, settings) {
		if since, ok := d.Alerts[id]; ok {
			active[id] = since
		} else {
			active[id] = now
			raised = append(raised, id)
		}
	}
	for id := range d.Alerts {
		if _, still := active[id]; !still {
			cleared = append(cleared, id)
		}
	}
	if len(raised) == 0 && len(cleared) == 0 {
		return nil, nil
	}
	if err := s.store.SetAlerts(ctx, d.ID, active); err != nil {
		s.log.Warn("alerts", "device", d.ID, "err", err)
		return nil, nil
	}
	for _, id := range raised {
		a, _ := k.Alert(id)
		_ = s.store.AddLog(ctx, d.UserID, d.ID, store.LogEntry{Actor: "device", Action: "alert", Key: id, Detail: alertText(a, d.Name, k, values)})
	}
	for _, id := range cleared {
		label := id
		if a, ok := k.Alert(id); ok {
			label = a.Label
		}
		_ = s.store.AddLog(ctx, d.UserID, d.ID, store.LogEntry{Actor: "device", Action: "alert_cleared", Key: id, Detail: label})
	}
	return raised, cleared
}

// alertText fills in an alert's message: {name}, {value}, {unit}.
func alertText(a *kinds.Alert, name string, k *kinds.Kind, values map[string]any) string {
	if a == nil {
		return ""
	}
	txt := a.Text
	if txt == "" {
		txt = a.Label
	}
	val, unit := "", ""
	if r, ok := k.Reading(a.Reading); ok {
		unit = r.Unit
		switch v := values[a.Reading].(type) {
		case float64:
			val = strconv.FormatFloat(v, 'f', r.Decimals, 64)
		case bool:
			val = r.Off
			if v {
				val = r.On
			}
		case string:
			val = v
		}
	}
	return strings.NewReplacer("{name}", name, "{value}", val, "{unit}", unit).Replace(txt)
}

// pathNumber reads a number at "a.b" in the report.
func pathNumber(all map[string]json.RawMessage, path string) (float64, bool) {
	head, rest, nested := strings.Cut(path, ".")
	v, ok := all[head]
	if !ok {
		return 0, false
	}
	if nested {
		var m map[string]json.RawMessage
		if json.Unmarshal(v, &m) != nil {
			return 0, false
		}
		return pathNumber(m, rest)
	}
	var f float64
	if json.Unmarshal(v, &f) != nil {
		return 0, false
	}
	return f, true
}

// afterSync tells the person what happened (events and notifications), in
// the background so the device gets its answer at once.
func (s *Server) afterSync(d *store.Device, k *kinds.Kind, out *store.SyncOut, raised, cleared []string, values map[string]any) {
	if !out.FirstSeen && !out.CameOnline && len(out.Refused) == 0 && len(out.NewEvents) == 0 && len(raised) == 0 && len(cleared) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		subject := fmt.Sprintf("device:%d", d.ID)
		base := map[string]any{"device_id": d.ID, "name": d.Name, "kind": d.Kind}
		with := func(extra map[string]any) map[string]any {
			m := map[string]any{}
			for k, v := range base {
				m[k] = v
			}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}
		if out.FirstSeen {
			s.publish(ctx, d.UserID, "devices.connected", subject, with(nil))
			s.notifyUser(ctx, d.UserID, "", d.Name+" is connected", "It checked in for the first time and now takes its settings from here.", "white_check_mark", d.ID)
		}
		if out.CameOnline {
			_ = s.store.AddLog(ctx, d.UserID, d.ID, store.LogEntry{Actor: "device", Action: "online", Detail: "checking in again"})
			s.publish(ctx, d.UserID, "devices.back_online", subject, with(nil))
			if d.NotifyOffline {
				s.notifyUser(ctx, d.UserID, "offline", d.Name+" is back", "It is checking in again.", "white_check_mark", d.ID)
			}
		}
		if len(out.Refused) > 0 {
			keys := make([]string, 0, len(out.Refused))
			for k, why := range out.Refused {
				keys = append(keys, k+" ("+why+")")
			}
			s.publish(ctx, d.UserID, "devices.setting_refused", subject, with(map[string]any{"refused": out.Refused}))
			s.notifyUser(ctx, d.UserID, "refused", d.Name+" refused a setting", strings.Join(keys, ", "), "warning", d.ID)
		}
		for _, e := range out.NewEvents {
			def, _ := k.Event(e.Type)
			s.publish(ctx, d.UserID, "devices.event", subject, with(map[string]any{"type": e.Type, "data": e.Data, "at": e.At}))
			if def != nil && def.Notify {
				s.notifyUser(ctx, d.UserID, "alerts", d.Name+": "+strings.ToLower(def.Label), def.Label+" at "+d.Name+".", "rotating_light", d.ID)
			}
		}
		for _, id := range raised {
			a, _ := k.Alert(id)
			text := alertText(a, d.Name, k, values)
			s.publish(ctx, d.UserID, "devices.alert", subject, with(map[string]any{"alert": id, "label": a.Label, "level": a.Level, "text": text}))
			tag := "warning"
			if a.Level == "alarm" {
				tag = "rotating_light"
			}
			s.notifyUser(ctx, d.UserID, "alerts", a.Label+": "+d.Name, text, tag, d.ID)
		}
		for _, id := range cleared {
			s.publish(ctx, d.UserID, "devices.alert_cleared", subject, with(map[string]any{"alert": id}))
		}
	}()
}

// checkOffline marks devices that stopped checking in (3 intervals + a little).
func (s *Server) checkOffline(ctx context.Context) {
	ds, err := s.store.OnlineDevices(ctx)
	if err != nil {
		return
	}
	for _, d := range ds {
		if deviceOnline(&d, time.Now()) {
			continue
		}
		if err := s.store.SetOnline(ctx, d.ID, false); err != nil {
			continue
		}
		_ = s.store.AddLog(ctx, d.UserID, d.ID, store.LogEntry{Actor: "device", Action: "offline", Detail: "stopped checking in"})
		s.publish(ctx, d.UserID, "devices.went_offline", fmt.Sprintf("device:%d", d.ID), map[string]any{"device_id": d.ID, "name": d.Name, "kind": d.Kind, "last_seen_at": d.LastSeenAt})
		if d.NotifyOffline {
			s.notifyUser(ctx, d.UserID, "offline", d.Name+" stopped checking in", "Last seen "+d.LastSeenAt[:16]+" UTC. It may be unplugged or off the WiFi.", "electric_plug", d.ID)
		}
	}
}

// deviceOnline: seen within three check-in intervals (plus 15 seconds).
func deviceOnline(d *store.Device, now time.Time) bool {
	if d.LastSeenAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, d.LastSeenAt)
	if err != nil {
		return false
	}
	poll := d.PollS
	if poll <= 0 {
		poll = 30
	}
	return now.Sub(t) <= time.Duration(3*poll)*time.Second+15*time.Second
}

// ---- rate limits ----

// limits slows down guessing (failed logins per address) and chatty devices
// (a small bucket per device: 6 calls, one more every 5 seconds).
type limits struct {
	mu      sync.Mutex
	fails   map[string]*failWindow
	buckets map[int64]*bucket
	now     func() time.Time
}

type failWindow struct {
	start time.Time
	n     int
}

type bucket struct {
	tokens float64
	at     time.Time
}

const (
	failMax     = 20
	failWindowD = 10 * time.Minute
	bucketSize  = 6
	bucketStep  = 5 * time.Second
)

func newLimits() *limits {
	return &limits{fails: map[string]*failWindow{}, buckets: map[int64]*bucket{}, now: time.Now}
}

func (l *limits) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.fails[ip]
	if f == nil || now.Sub(f.start) > failWindowD {
		f = &failWindow{start: now}
		l.fails[ip] = f
	}
	f.n++
}

func (l *limits) failWait(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil || f.n < failMax {
		return 0
	}
	left := failWindowD - l.now().Sub(f.start)
	if left <= 0 {
		delete(l.fails, ip)
		return 0
	}
	return left
}

func (l *limits) deviceWait(id int64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[id]
	if b == nil {
		b = &bucket{tokens: bucketSize, at: now}
		l.buckets[id] = b
	}
	b.tokens += float64(now.Sub(b.at)) / float64(bucketStep)
	if b.tokens > bucketSize {
		b.tokens = bucketSize
	}
	b.at = now
	if b.tokens < 1 {
		return time.Duration((1 - b.tokens) * float64(bucketStep))
	}
	b.tokens--
	return 0
}

// sweep forgets old entries.
func (l *limits) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for ip, f := range l.fails {
		if now.Sub(f.start) > failWindowD {
			delete(l.fails, ip)
		}
	}
	for id, b := range l.buckets {
		if now.Sub(b.at) > time.Hour {
			delete(l.buckets, id)
		}
	}
}
