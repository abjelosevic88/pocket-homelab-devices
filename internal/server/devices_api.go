package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// ---- views ----

// deviceView is a device as the web UI and linked apps see it.
type deviceView struct {
	*store.Device
	KindName string         `json:"kind_name"`
	State    string         `json:"state"` // online | offline | new
	Pending  int            `json:"pending"`
	Queued   int            `json:"queued"`
	Summary  map[string]any `json:"summary"`  // what every device reports: fw, uptime, WiFi
	Readings map[string]any `json:"readings"` // the kind's readings, by key
	Alerts   []alertView    `json:"alerts"`   // alerts that hold now
	Icon     string         `json:"icon"`
	Category string         `json:"category"`
	Power    string         `json:"power"`
}

type alertView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Level  string `json:"level"`
	Text   string `json:"text"`
	Since  string `json:"since"`
	Action string `json:"action,omitempty"`
}

// settingView is one row of a device's settings: what you want next to what it has.
type settingView struct {
	Key       string  `json:"key"`
	Desired   *string `json:"desired"`  // nil = not set here (a secret: never shown)
	Reported  *string `json:"reported"` // nil = not reported yet
	SecretSet bool    `json:"secret_set,omitempty"`
	Pending   bool    `json:"pending"`
	Error     string  `json:"error,omitempty"`
	ChangedAt string  `json:"changed_at,omitempty"`
}

func (s *Server) view(ctx context.Context, d *store.Device, now time.Time) deviceView {
	v := deviceView{Device: d, State: "new", Summary: map[string]any{}}
	k, ok := kinds.Get(d.Kind)
	if ok {
		v.KindName = k.Name
	}
	if d.LastSeenAt != "" {
		v.State = "offline"
		if deviceOnline(d, now) {
			v.State = "online"
		}
	}
	if ok && d.DesiredRev > d.AppliedRev {
		desired, _ := s.store.Desired(ctx, d.UserID, d.ID)
		v.Pending = len(store.Diff(k, desired, d.Reported, d.AppliedRev))
		if v.Pending == 0 {
			v.Pending = 1 // a new revision the device has not taken yet
		}
	}
	v.Queued = s.store.QueuedCount(ctx, d.ID)
	var st struct {
		FW      string `json:"fw"`
		UptimeS *int64 `json:"uptime_s"`
		Reset   string `json:"reset_reason"`
		WiFi    *struct {
			SSID string `json:"ssid"`
			RSSI *int   `json:"rssi"`
			IP   string `json:"ip"`
		} `json:"wifi"`
	}
	_ = json.Unmarshal(d.Status, &st)
	if st.FW != "" {
		v.Summary["fw"] = st.FW
	}
	if st.UptimeS != nil {
		v.Summary["uptime_s"] = *st.UptimeS
	}
	if st.Reset != "" {
		v.Summary["reset_reason"] = st.Reset
	}
	if st.WiFi != nil {
		v.Summary["ssid"], v.Summary["ip"] = st.WiFi.SSID, st.WiFi.IP
		if st.WiFi.RSSI != nil {
			v.Summary["rssi"] = *st.WiFi.RSSI
		}
	}
	v.Readings, v.Alerts = map[string]any{}, []alertView{}
	if ok {
		v.Icon, v.Category, v.Power = k.Icon, k.Category, k.Power
		var status map[string]any
		_ = json.Unmarshal(d.Status, &status)
		v.Readings = k.Values(status, d.Reported)
		for _, a := range k.Alerts {
			since, on := d.Alerts[a.ID]
			if !on {
				continue
			}
			v.Alerts = append(v.Alerts, alertView{ID: a.ID, Label: a.Label, Level: a.Level, Text: alertText(&a, d.Name, k, v.Readings), Since: since, Action: a.Action})
		}
	}
	return v
}

func (s *Server) settingsView(ctx context.Context, d *store.Device, k *kinds.Kind) []settingView {
	desired, _ := s.store.Desired(ctx, d.UserID, d.ID)
	pending := map[string]bool{}
	if d.DesiredRev > d.AppliedRev {
		for key := range store.Diff(k, desired, d.Reported, d.AppliedRev) {
			pending[key] = true
		}
	}
	out := make([]settingView, 0, len(k.Settings))
	for _, st := range k.Settings {
		v := settingView{Key: st.Key, Pending: pending[st.Key], Error: d.ConfigErrors[st.Key]}
		if dv, ok := desired[st.Key]; ok {
			v.ChangedAt = dv.UpdatedAt
			if st.Secret {
				v.SecretSet = true
			} else {
				val := dv.Value
				v.Desired = &val
			}
		}
		if rv, ok := d.Reported[st.Key]; ok {
			val := rv
			if st.Secret && val != "" {
				val = kinds.SecretMask
			}
			v.Reported = &val
		}
		out = append(out, v)
	}
	return out
}

// deviceURL is the address devices are told to use.
func (s *Server) deviceURL(ctx context.Context) string {
	st, _ := s.store.GetSettings(ctx, 0)
	if st.DeviceURL != "" {
		return st.DeviceURL
	}
	return s.cfg.BaseURL
}

// setupLines are the console lines to paste into the device.
func setupLines(k *kinds.Kind, url, token string) []string {
	out := make([]string, 0, len(k.Setup))
	for _, l := range k.Setup {
		out = append(out, strings.NewReplacer("{url}", url, "{token}", token).Replace(l))
	}
	return out
}

// actorName is who is acting: a person's name, or the linked app.
func actorName(ctx context.Context) string {
	if a := linkApp(ctx); a != "" {
		return "Pocket " + strings.ToUpper(a[:1]) + a[1:]
	}
	if u := userFrom(ctx); u != nil {
		return firstNonEmpty(u.Name, u.Email)
	}
	return ""
}

// ---- handlers ----

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, kinds.All())
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ds, err := s.store.ListDevices(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "devices")
		return
	}
	now := time.Now()
	out := make([]deviceView, 0, len(ds))
	for i := range ds {
		out = append(out, s.view(ctx, &ds[i], now))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOverview is the short summary for dashboards and linked apps.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ds, err := s.store.ListDevices(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "devices")
		return
	}
	now := time.Now()
	counts := map[string]int{"total": len(ds), "online": 0, "offline": 0, "new": 0, "pending": 0, "alerts": 0}
	for i := range ds {
		v := s.view(ctx, &ds[i], now)
		counts[v.State]++
		if v.Pending > 0 {
			counts["pending"]++
		}
		if len(v.Alerts) > 0 {
			counts["alerts"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

func (s *Server) deviceDetail(ctx context.Context, d *store.Device) map[string]any {
	k, _ := kinds.Get(d.Kind)
	out := map[string]any{"device": s.view(ctx, d, time.Now()), "server_url": s.deviceURL(ctx)}
	if k != nil {
		out["settings"] = s.settingsView(ctx, d, k)
	}
	return out
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := s.store.GetDevice(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	writeJSON(w, http.StatusOK, s.deviceDetail(ctx, d))
}

type deviceIn struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	HWID          string `json:"hw_id"`
	Location      string `json:"location"`
	Notes         string `json:"notes"`
	PollS         int    `json:"poll_s"`
	NotifyOffline *bool  `json:"notify_offline"`
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in deviceIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	k, ok := kinds.Get(in.Kind)
	if !ok {
		ids := make([]string, 0, len(kinds.All()))
		for _, x := range kinds.All() {
			ids = append(ids, x.ID)
		}
		writeErr(w, http.StatusBadRequest, "kind is one of "+strings.Join(ids, ", "))
		return
	}
	hwid, err := k.NormalizeHWID(in.HWID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = k.Name
	}
	if len([]rune(in.Name)) > 80 || len(in.Location) > 80 || len(in.Notes) > 4000 {
		writeErr(w, http.StatusBadRequest, "name and place are at most 80 characters")
		return
	}
	token := store.RandomToken(20)
	d := &store.Device{Kind: k.ID, Name: in.Name, HWID: hwid, Location: strings.TrimSpace(in.Location), Notes: strings.TrimSpace(in.Notes),
		TokenHash: store.HashToken(token), TokenHint: token[:6], PollS: k.Poll.Default, NotifyOffline: in.NotifyOffline == nil || *in.NotifyOffline}
	if err := s.store.CreateDevice(ctx, actorID(ctx), d, actorName(ctx)); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "a device with this "+strings.ToLower(k.HWID.Label)+" is already on this server")
			return
		}
		s.fail(w, err, "add device")
		return
	}
	url := s.deviceURL(ctx)
	out := s.deviceDetail(ctx, d)
	out["token"], out["setup"], out["setup_help"] = token, setupLines(k, url, token), k.SetupHelp
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	d, err := s.store.GetDevice(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	var in deviceIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	k, _ := kinds.Get(d.Kind)
	oldName := d.Name
	if keys["name"] {
		if n := strings.TrimSpace(in.Name); n != "" && len([]rune(n)) <= 80 {
			d.Name = n
		} else {
			writeErr(w, http.StatusBadRequest, "a name of 1 to 80 characters")
			return
		}
	}
	if keys["location"] {
		d.Location = strings.TrimSpace(in.Location)
	}
	if keys["notes"] {
		d.Notes = strings.TrimSpace(in.Notes)
	}
	if keys["poll_s"] {
		lo, hi := 10, 3600
		if k != nil {
			lo, hi = k.Poll.Min, k.Poll.Max
		}
		if in.PollS < lo || in.PollS > hi {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("check in every %d to %d seconds", lo, hi))
			return
		}
		d.PollS = in.PollS
	}
	if keys["notify_offline"] && in.NotifyOffline != nil {
		d.NotifyOffline = *in.NotifyOffline
	}
	if err := s.store.UpdateDevice(ctx, uid, d); err != nil {
		s.fail(w, err, "save device")
		return
	}
	if d.Name != oldName {
		_ = s.store.AddLog(ctx, uid, d.ID, store.LogEntry{Actor: actorName(ctx), Action: "renamed", Old: oldName, New: d.Name})
	}
	writeJSON(w, http.StatusOK, s.deviceDetail(ctx, d))
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteDevice(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete device")
		return
	}
	s.demoMu.Lock()
	delete(s.demo, idParam(r, "id"))
	s.demoMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleNewToken replaces a device's token; the old one stops working at once.
func (s *Server) handleNewToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	d, err := s.store.GetDevice(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	k, _ := kinds.Get(d.Kind)
	token := store.RandomToken(20)
	if err := s.store.SetDeviceToken(ctx, uid, d.ID, store.HashToken(token), token[:6]); err != nil {
		s.fail(w, err, "new token")
		return
	}
	_ = s.store.AddLog(ctx, uid, d.ID, store.LogEntry{Actor: actorName(ctx), Action: "token", Detail: "new token, the old one stopped working"})
	d, _ = s.store.GetDevice(ctx, uid, d.ID)
	out := s.deviceDetail(ctx, d)
	out["token"] = token
	if k != nil {
		out["setup"], out["setup_help"] = setupLines(k, s.deviceURL(ctx), token), k.SetupHelp
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePutSettings sets desired values: {"values": {key: value}, "reset":
// [keys]} (reset = back to the device's default). Everything is checked
// first with the device's own rules; nothing is saved when one is wrong.
func (s *Server) handlePutDeviceSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	d, err := s.store.GetDevice(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	k, ok := kinds.Get(d.Kind)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown kind of device")
		return
	}
	var in struct {
		Values map[string]any `json:"values"`
		Reset  []string       `json:"reset"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	values := map[string]string{}
	errs := map[string]string{}
	resets := map[string]bool{}
	for _, key := range in.Reset {
		st, ok := k.Setting(key)
		switch {
		case !ok:
			errs[key] = "unknown setting"
		case st.Locked:
			errs[key] = "only changed at the device"
		case !st.DefaultOK:
			errs[key] = "has no default to go back to"
		default:
			values[key] = st.Default
			if n, err := st.Normalize(st.Default); err == nil {
				values[key] = n
			}
			resets[key] = true
		}
	}
	for key, raw := range in.Values {
		st, ok := k.Setting(key)
		if !ok {
			errs[key] = "unknown setting"
			continue
		}
		if st.Locked {
			errs[key] = "only changed at the device"
			continue
		}
		v, ok := settingString(raw)
		if !ok {
			errs[key] = "not a value"
			continue
		}
		if st.Secret && v == kinds.SecretMask {
			continue // unchanged
		}
		n, err := st.Normalize(v)
		if err != nil {
			errs[key] = err.Error()
			continue
		}
		values[key] = n
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "some values are not right", "errors": errs})
		return
	}
	actor := actorName(ctx)
	if len(values) > 0 {
		plain, reset := map[string]string{}, map[string]string{}
		for key, v := range values {
			if resets[key] {
				reset[key] = v
			} else {
				plain[key] = v
			}
		}
		if len(plain) > 0 {
			if _, _, err := s.store.SetDesired(ctx, uid, d.ID, k, plain, actor, "setting"); err != nil {
				s.fail(w, err, "save settings")
				return
			}
		}
		if len(reset) > 0 {
			if _, _, err := s.store.SetDesired(ctx, uid, d.ID, k, reset, actor, "reset"); err != nil {
				s.fail(w, err, "save settings")
				return
			}
		}
	}
	d, _ = s.store.GetDevice(ctx, uid, d.ID)
	s.demoNudge(d.ID)
	writeJSON(w, http.StatusOK, s.deviceDetail(ctx, d))
}

// settingString accepts a JSON string, number or bool as a console value.
func settingString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		if x {
			return "on", true
		}
		return "off", true
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return fmt.Sprintf("%d", int64(x)), true
		}
		return fmt.Sprintf("%g", x), true
	}
	return "", false
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, err := s.store.GetDevice(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "device")
		return
	}
	cs, err := s.store.ListCommands(ctx, uid, idParam(r, "id"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "commands")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// handleCommand queues a command. Dangerous ones need "confirm": true, the
// erase also "confirm_again": true, and a linked app may send none of them.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	d, err := s.store.GetDevice(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	k, ok := kinds.Get(d.Kind)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown kind of device")
		return
	}
	var in struct {
		Name         string         `json:"name"`
		Args         map[string]any `json:"args"`
		Confirm      bool           `json:"confirm"`
		ConfirmAgain bool           `json:"confirm_again"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, ok := k.Command(in.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "this device does not know that command")
		return
	}
	if c.Danger > 0 && linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "only you can "+strings.ToLower(c.Label)+", not a linked app")
		return
	}
	if (c.Danger >= 1 && !in.Confirm) || (c.Danger >= 2 && !in.ConfirmAgain) {
		writeErr(w, http.StatusBadRequest, "please confirm: "+strings.ToLower(c.Label))
		return
	}
	args := map[string]any{}
	for _, a := range c.Args {
		raw, ok := in.Args[a.Name]
		if !ok {
			args[a.Name] = int(a.Default)
			if a.DefaultSetting != "" {
				if n, err := strconv.Atoi(d.Reported[a.DefaultSetting]); err == nil && float64(n) >= a.Min && float64(n) <= a.Max {
					args[a.Name] = n
				}
			}
			continue
		}
		f, ok := raw.(float64)
		if !ok || f != math.Trunc(f) || f < a.Min || f > a.Max {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("%s is %g to %g", strings.ToLower(a.Label), a.Min, a.Max))
			return
		}
		args[a.Name] = int(f)
	}
	cmd, err := s.store.QueueCommand(ctx, uid, d.ID, c.Name, args, actorName(ctx))
	if err != nil {
		s.fail(w, err, "command")
		return
	}
	s.demoNudge(d.ID)
	writeJSON(w, http.StatusCreated, cmd)
}

func (s *Server) handleCancelCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.CancelCommand(ctx, actorID(ctx), idParam(r, "id"), idParam(r, "cid")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusConflict, "already sent, or not found")
			return
		}
		s.fail(w, err, "cancel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeviceLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, err := s.store.GetDevice(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "device")
		return
	}
	l, err := s.store.ListLog(ctx, uid, idParam(r, "id"), int64(queryInt(r, "before")), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// handleReadings returns the 5-minute history of a device's chart readings.
func (s *Server) handleReadings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, err := s.store.GetDevice(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "device")
		return
	}
	hours := queryInt(r, "hours")
	if hours <= 0 || hours > 24*30 {
		hours = 48
	}
	rs, err := s.store.Readings(ctx, uid, idParam(r, "id"), time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		s.fail(w, err, "readings")
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// handleEvents returns what a device told as it happened, newest first.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, err := s.store.GetDevice(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "device")
		return
	}
	es, err := s.store.ListEvents(ctx, uid, idParam(r, "id"), int64(queryInt(r, "before")), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "events")
		return
	}
	writeJSON(w, http.StatusOK, es)
}

// handlePosixTZ turns an IANA zone ("Europe/Sarajevo") into the POSIX
// string a device understands, from the zone file's footer.
func (s *Server) handlePosixTZ(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")
	tz, err := posixTZ(zone)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"zone": zone, "tz": tz})
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
