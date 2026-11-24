package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"device_url": st.DeviceURL, "device_url_effective": firstNonEmpty(st.DeviceURL, s.cfg.BaseURL),
		"notify_offline": st.NotifyOffline, "notify_refused": st.NotifyRefused, "notify_alerts": st.NotifyAlerts,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"weather_url": st.WeatherURL, "weather_token": mask(st.WeatherToken), "weather_public_url": st.WeatherPublicURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, _, _, public := connSettings(st, a)
		if public == "" && via != "" {
			public = s.resolveSibling(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// and device address are only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["device_url"] && isAdmin {
		v := strings.TrimRight(strings.TrimSpace(in.DeviceURL), "/")
		if v != "" {
			k, _ := kinds.Get("ntp-clock")
			if _, err := k.Normalize("server_url", v); err != nil {
				writeErr(w, http.StatusBadRequest, "the device address must be "+err.Error())
				return
			}
			if u, _ := url.Parse(v); u == nil || u.Path != "" {
				writeErr(w, http.StatusBadRequest, "the device address is only https:// and the host name")
				return
			}
		}
		cur.DeviceURL = v
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	urlIn := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boolIn("notify_offline", &cur.NotifyOffline, in.NotifyOffline)
	boolIn("notify_refused", &cur.NotifyRefused, in.NotifyRefused)
	boolIn("notify_alerts", &cur.NotifyAlerts, in.NotifyAlerts)
	if keys["notify_url"] {
		cur.NotifyURL = strings.TrimSpace(in.NotifyURL)
	}
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	urlIn("weather_url", &cur.WeatherURL, in.WeatherURL)
	secret("weather_token", &cur.WeatherToken, in.WeatherToken)
	urlIn("weather_public_url", &cur.WeatherPublicURL, in.WeatherPublicURL)
	if keys["weather_mode"] && (in.WeatherMode == modeCore || in.WeatherMode == modeDirect || in.WeatherMode == modeOff) {
		cur.WeatherMode = in.WeatherMode
	}
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && (keys["title"] || keys["device_url"]) {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for _, k := range modeKeys {
		if keys[k] {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				var ps []pocket.Place
				ps, err = conn.client.Places(ctx)
				msg = fmt.Sprintf("%s %s, %s, %d places", a.name, v, how, len(ps))
			}
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Devices", Body: "Notifications work. You will hear here when a device stops checking in.", Priority: "info", Tags: []string{"electric_plug"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Devices", Body: "Notifications work.", Priority: "info", Tags: []string{"electric_plug"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "syncs": s.syncs.Load(),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-devices-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDevice is one device in the JSON export: the settings set here and
// the token hash, so a device keeps working when it moves to another box
// (only its server_url changes). Secrets (the WiFi password) are left out.
type exportDevice struct {
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	HWID          string                 `json:"hw_id"`
	Location      string                 `json:"location"`
	Notes         string                 `json:"notes"`
	PollS         int                    `json:"poll_s"`
	NotifyOffline bool                   `json:"notify_offline"`
	TokenHash     string                 `json:"token_hash"`
	TokenHint     string                 `json:"token_hint"`
	DesiredRev    int64                  `json:"desired_rev"`
	Settings      []store.DesiredSetting `json:"settings"`
	Reported      map[string]string      `json:"reported"`
	LastSeenAt    string                 `json:"last_seen_at"`
	CreatedAt     string                 `json:"created_at"`
}

// exportDoc is the JSON export of one person's data.
type exportDoc struct {
	App      string         `json:"app"`
	Version  string         `json:"version"`
	Exported string         `json:"exported_at"`
	Devices  []exportDevice `json:"devices"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-devices", Version: Version, Exported: store.Now(), Devices: []exportDevice{}}
	ds, err := s.store.ListDevices(ctx, uid)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	for _, d := range ds {
		if d.Demo {
			continue
		}
		k, _ := kinds.Get(d.Kind)
		e := exportDevice{Kind: d.Kind, Name: d.Name, HWID: d.HWID, Location: d.Location, Notes: d.Notes, PollS: d.PollS, NotifyOffline: d.NotifyOffline,
			TokenHash: d.TokenHash, TokenHint: d.TokenHint, DesiredRev: d.DesiredRev, Settings: []store.DesiredSetting{}, Reported: map[string]string{},
			LastSeenAt: d.LastSeenAt, CreatedAt: d.CreatedAt}
		desired, _ := s.store.Desired(ctx, uid, d.ID)
		for _, key := range sortedKeysDesired(desired) {
			if st, ok := k.Setting(key); k != nil && ok && st.Secret {
				continue
			}
			e.Settings = append(e.Settings, desired[key])
		}
		for key, v := range d.Reported {
			if st, ok := k.Setting(key); k != nil && ok && st.Secret {
				continue
			}
			e.Reported[key] = v
		}
		doc.Devices = append(doc.Devices, e)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-devices-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

func sortedKeysDesired(m map[string]store.DesiredSetting) []string {
	plain := make(map[string]string, len(m))
	for k := range m {
		plain[k] = ""
	}
	return sortedKeys(plain)
}

// handleExportCSV writes every device as a CSV row.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ds, err := s.store.ListDevices(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-devices.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "kind", "hw_id", "location", "state", "last_seen_at", "firmware", "ip", "poll_s", "desired_rev", "applied_rev", "notes", "created_at"})
	now := time.Now()
	for i := range ds {
		v := s.view(ctx, &ds[i], now)
		fw, _ := v.Summary["fw"].(string)
		ip, _ := v.Summary["ip"].(string)
		_ = cw.Write([]string{ds[i].Name, ds[i].Kind, ds[i].HWID, ds[i].Location, v.State, ds[i].LastSeenAt, fw, ip, strconv.Itoa(ds[i].PollS),
			strconv.FormatInt(ds[i].DesiredRev, 10), strconv.FormatInt(ds[i].AppliedRev, 10), ds[i].Notes, ds[i].CreatedAt})
	}
	cw.Flush()
}

// handleImportJSON adds the devices of an export to the caller's own. It
// adds, never changes: a device whose hardware id is already on this box is
// skipped, so importing the same file twice adds nothing. Devices keep their
// token, so they only need the new server_url.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-devices" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Devices export")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{"devices": 0, "already": 0, "skipped": 0}
	for _, e := range doc.Devices {
		k, ok := kinds.Get(e.Kind)
		if !ok {
			counts["skipped"]++
			continue
		}
		hwid, err := k.NormalizeHWID(e.HWID)
		if err != nil || len(e.TokenHash) != 64 || strings.TrimSpace(e.Name) == "" {
			counts["skipped"]++
			continue
		}
		if _, err := s.store.DeviceByHWID(ctx, hwid); err == nil {
			counts["already"]++
			continue
		}
		poll := e.PollS
		if poll < k.Poll.Min || poll > k.Poll.Max {
			poll = k.Poll.Default
		}
		d := &store.Device{Kind: k.ID, Name: strings.TrimSpace(e.Name), HWID: hwid, Location: e.Location, Notes: e.Notes, PollS: poll,
			NotifyOffline: e.NotifyOffline, TokenHash: strings.ToLower(e.TokenHash), TokenHint: e.TokenHint}
		if err := s.store.CreateDevice(ctx, uid, d, "import"); err != nil {
			counts["skipped"]++
			continue
		}
		values := map[string]string{}
		for _, st := range e.Settings {
			row, ok := k.Setting(st.Key)
			if !ok || row.Locked || row.Secret {
				continue
			}
			if n, err := row.Normalize(st.Value); err == nil {
				values[st.Key] = n
			}
		}
		if len(values) > 0 {
			_, _, _ = s.store.SetDesired(ctx, uid, d.ID, k, values, "import", "setting")
		}
		counts["devices"]++
	}
	writeJSON(w, http.StatusOK, counts)
}
