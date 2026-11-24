package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/clocksim"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/config"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

type app struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	alice string
	bob   string
	ids   map[string]int64
}

func newApp(t *testing.T, tweak func(*config.Config)) *app {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.BaseURL = "https://devices.test"
	cfg.MaxBodyBytes = 16 << 20
	if tweak != nil {
		tweak(cfg)
	}
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &app{t: t, st: st, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *app) call(auth, method, path string, body any) (int, map[string]any, []any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, a.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return res.StatusCode, m, l
}

func (a *app) ok(auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code >= 300 {
		a.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (a *app) expect(want int, auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code != want {
		a.t.Fatalf("%s %s → %d, want %d (%v)", method, path, code, want, m)
	}
	return m
}

func num(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }

// addClock adds a clock for a person and returns its id, token and a simulator.
func (a *app) addClock(auth, mac string) (int64, string, *clocksim.Clock, *clocksim.Client) {
	a.t.Helper()
	m := a.expect(201, auth, "POST", "/api/v1/devices", map[string]any{"kind": "ntp-clock", "name": "Clock " + mac[:4], "hw_id": mac, "location": "Bedroom"})
	tok, _ := m["token"].(string)
	if len(tok) < 32 || len(tok) > 64 {
		a.t.Fatalf("token %q is not 32..64 characters", tok)
	}
	setup, _ := m["setup"].([]any)
	if len(setup) != 3 || setup[0] != "cfg set server_url https://devices.test" || setup[1] != "cfg set device_token "+tok || setup[2] != "cfg set remote on" {
		a.t.Fatalf("setup lines: %v", setup)
	}
	dev := m["device"].(map[string]any)
	c := clocksim.New(strings.ToUpper(strings.ReplaceAll(mac, ":", "")), 7)
	c.Set("ssid", "Home")
	c.Set("pass", "secret-wifi")
	return int64(num(dev, "id")), tok, c, &clocksim.Client{URL: a.url, Token: tok}
}

func (a *app) sync(cl *clocksim.Client, c *clocksim.Clock) int {
	a.t.Helper()
	code, _, err := cl.Sync(context.Background(), c)
	if err != nil && code == 0 {
		a.t.Fatal(err)
	}
	return code
}

// rawSync posts a body to the device endpoint.
func (a *app) rawSync(token string, body []byte) (*http.Response, map[string]any) {
	a.t.Helper()
	req, _ := http.NewRequest("POST", a.url+"/api/v1/device/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res, m
}

func settingOf(t *testing.T, detail map[string]any, key string) map[string]any {
	t.Helper()
	for _, x := range detail["settings"].([]any) {
		m := x.(map[string]any)
		if m["key"] == key {
			return m
		}
	}
	t.Fatalf("setting %s missing", key)
	return nil
}

// A clock connects, takes a change within one check-in, reports it back and
// the "pending" marker goes away.
func TestClockTakesSettings(t *testing.T) {
	a := newApp(t, nil)
	id, _, c, cl := a.addClock(a.alice, "a1:b2:c3:d4:e5:f6")
	path := fmt.Sprintf("/api/v1/devices/%d", id)

	if got := a.ok(a.alice, "GET", path, nil)["device"].(map[string]any)["state"]; got != "new" {
		t.Fatalf("state before the first check-in: %v", got)
	}
	if code := a.sync(cl, c); code != 200 {
		t.Fatalf("first sync: %d", code)
	}
	d := a.ok(a.alice, "GET", path, nil)
	if dev := d["device"].(map[string]any); dev["state"] != "online" || num(dev, "pending") != 0 {
		t.Fatalf("after the first check-in: %v", dev)
	}
	if s := settingOf(t, d, "ssid"); s["reported"] != "Home" || s["desired"] != nil {
		t.Fatalf("ssid row: %v", s)
	}
	if s := settingOf(t, d, "pass"); s["reported"] != "****" {
		t.Fatalf("a secret reached the browser: %v", s)
	}

	d = a.ok(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"brightness": 60, "alarm": true, "alarm_time": "6:30", "pass": "new-password"}})
	if dev := d["device"].(map[string]any); num(dev, "pending") == 0 {
		t.Fatalf("no pending changes after a change: %v", dev)
	}
	if s := settingOf(t, d, "alarm_time"); s["desired"] != "06:30" || s["pending"] != true {
		t.Fatalf("alarm_time row: %v", s)
	}
	if s := settingOf(t, d, "pass"); s["desired"] != nil || s["secret_set"] != true {
		t.Fatalf("pass row shows the secret: %v", s)
	}
	a.sync(cl, c)
	if c.Get("brightness") != "60" || c.Get("alarm") != "on" || c.Get("alarm_time") != "06:30" || c.Get("pass") != "new-password" {
		t.Fatalf("clock did not apply: brightness=%s alarm=%s time=%s pass ok=%v", c.Get("brightness"), c.Get("alarm"), c.Get("alarm_time"), c.Get("pass") == "new-password")
	}
	a.sync(cl, c) // reports what it has now
	d = a.ok(a.alice, "GET", path, nil)
	if dev := d["device"].(map[string]any); num(dev, "pending") != 0 || num(dev, "applied_rev") != num(dev, "desired_rev") {
		t.Fatalf("still pending after the clock applied it: %v", dev)
	}

	// in step, nothing to send: config is left out of the answer
	body, _ := json.Marshal(c.Request())
	_, m := a.rawSync(cl.Token, body)
	if _, ok := m["config"]; ok {
		t.Fatalf("config sent although in step: %v", m)
	}
	if num(m, "poll_s") != 30 || m["time"] == nil {
		t.Fatalf("answer: %v", m)
	}
}

// Only the differing settings go out, secrets only when they changed after
// the revision the clock applied; locked ones never.
func TestDiffAndLocked(t *testing.T) {
	a := newApp(t, nil)
	id, _, c, cl := a.addClock(a.alice, "A1B2C3D4E5F7")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.sync(cl, c)
	code, m, _ := a.call(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"server_url": "https://evil.example.com"}})
	if code != 400 || !strings.Contains(fmt.Sprint(m["errors"]), "server_url") {
		t.Fatalf("a locked setting was accepted: %d %v", code, m)
	}
	code, m, _ = a.call(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"brightness": 0, "tz": "Europe/Sarajevo", "chime": false}})
	if code != 400 || m["errors"].(map[string]any)["brightness"] == nil || m["errors"].(map[string]any)["tz"] == nil {
		t.Fatalf("wrong values: %d %v", code, m)
	}
	if d := a.ok(a.alice, "GET", path, nil)["device"].(map[string]any); num(d, "desired_rev") != 0 {
		t.Fatalf("a refused request changed the revision: %v", d["desired_rev"])
	}
	a.ok(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"brightness": "100", "chime": "off", "pass": "another-pass"}})
	req := c.Request()
	body, _ := json.Marshal(req)
	_, ans := a.rawSync(cl.Token, body)
	cfg, _ := ans["config"].(map[string]any)
	// brightness 100 is the clock's value already: not sent
	if len(cfg) != 2 || cfg["chime"] != "off" || cfg["pass"] != "another-pass" {
		t.Fatalf("diff: %v", cfg)
	}
	// the answer was lost: the next poll gets the same again (idempotent)
	_, ans = a.rawSync(cl.Token, body)
	if cfg2, _ := ans["config"].(map[string]any); len(cfg2) != 2 {
		t.Fatalf("second poll: %v", cfg2)
	}
	// once applied, the secret is not sent again
	c.Apply(toResp(ans))
	body, _ = json.Marshal(c.Request())
	_, ans = a.rawSync(cl.Token, body)
	if _, ok := ans["config"]; ok {
		t.Fatalf("sent again after applying: %v", ans)
	}
}

// A change made at the clock (console) while in step wins and is logged;
// a refused value is logged with the clock's reason.
func TestConsoleChangeAndRefusal(t *testing.T) {
	a := newApp(t, nil)
	id, _, c, cl := a.addClock(a.alice, "A1B2C3D4E5F8")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.ok(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"brightness": 50}})
	a.sync(cl, c)
	a.sync(cl, c)
	c.Set("brightness", "75") // cfg set brightness 75 at the console
	a.sync(cl, c)
	d := a.ok(a.alice, "GET", path, nil)
	if s := settingOf(t, d, "brightness"); s["desired"] != "75" || s["pending"] != false {
		t.Fatalf("console change not taken: %v", s)
	}
	_, _, l := a.call(a.alice, "GET", path+"/log", nil)
	if !strings.Contains(fmt.Sprint(l), "device_changed") {
		t.Fatalf("log: %v", l)
	}

	// a refusal: the clock keeps its value and says why
	req := c.Request()
	req.ConfigErrors = map[string]string{"alarm_time": "out of range"}
	body, _ := json.Marshal(req)
	a.rawSync(cl.Token, body)
	_, _, l = a.call(a.alice, "GET", path+"/log", nil)
	if !strings.Contains(fmt.Sprint(l), "out of range") {
		t.Fatalf("refusal not logged: %v", l)
	}
	if d := a.ok(a.alice, "GET", path, nil)["device"].(map[string]any); d["config_errors"].(map[string]any)["alarm_time"] != "out of range" {
		t.Fatalf("config errors: %v", d["config_errors"])
	}
}

// After cfg_reset the clock comes back with revision 0 and gets everything
// set here again, the WiFi password included.
func TestEraseResendsEverything(t *testing.T) {
	a := newApp(t, nil)
	id, _, c, cl := a.addClock(a.alice, "A1B2C3D4E5F9")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.ok(a.alice, "PUT", path+"/settings", map[string]any{"values": map[string]any{"ssid": "Home", "pass": "wifi-pass-1", "alarm": "on"}})
	a.sync(cl, c)
	a.expect(400, a.alice, "POST", path+"/commands", map[string]any{"name": "cfg_reset", "confirm": true})
	a.expect(201, a.alice, "POST", path+"/commands", map[string]any{"name": "cfg_reset", "confirm": true, "confirm_again": true})
	a.sync(cl, c) // gets the command, erases
	if c.Rev() != 0 || c.Get("alarm") != "off" || c.Get("pass") != "" {
		t.Fatalf("not erased: rev %d alarm %s", c.Rev(), c.Get("alarm"))
	}
	a.sync(cl, c) // reports revision 0 → everything again
	if c.Get("alarm") != "on" || c.Get("pass") != "wifi-pass-1" || c.Get("ssid") != "Home" {
		t.Fatalf("not restored: alarm %s ssid %s", c.Get("alarm"), c.Get("ssid"))
	}
}

// Commands go out once, in order, and their results come back.
func TestCommandsAtMostOnce(t *testing.T) {
	a := newApp(t, nil)
	id, _, c, cl := a.addClock(a.alice, "A1B2C3D4E5FA")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.expect(201, a.alice, "POST", path+"/commands", map[string]any{"name": "sync_time"})
	a.expect(201, a.alice, "POST", path+"/commands", map[string]any{"name": "alarm_test", "args": map[string]any{"n": 3}})
	a.expect(400, a.alice, "POST", path+"/commands", map[string]any{"name": "alarm_test", "args": map[string]any{"n": 9}})
	a.expect(400, a.alice, "POST", path+"/commands", map[string]any{"name": "launch_rockets"})
	a.expect(400, a.alice, "POST", path+"/commands", map[string]any{"name": "reboot"})
	queued := a.expect(201, a.alice, "POST", path+"/commands", map[string]any{"name": "weather_refresh"})
	a.expect(204, a.alice, "DELETE", fmt.Sprintf("%s/commands/%d", path, int64(num(queued, "id"))), nil)

	body, _ := json.Marshal(c.Request())
	_, ans := a.rawSync(cl.Token, body)
	cmds, _ := ans["commands"].([]any)
	if len(cmds) != 2 || cmds[0].(map[string]any)["name"] != "sync_time" || cmds[1].(map[string]any)["name"] != "alarm_test" || num(cmds[1].(map[string]any), "n") != 3 {
		t.Fatalf("commands: %v", cmds)
	}
	_, again := a.rawSync(cl.Token, body)
	if again["commands"] != nil {
		t.Fatalf("delivered twice: %v", again["commands"])
	}
	c.Apply(toResp(ans))
	a.sync(cl, c) // reports the results
	_, _, l := a.call(a.alice, "GET", path+"/commands", nil)
	states := map[string]string{}
	for _, x := range l {
		m := x.(map[string]any)
		states[m["name"].(string)] = m["state"].(string)
	}
	if states["sync_time"] != "done" || states["alarm_test"] != "done" || states["weather_refresh"] != "cancelled" {
		t.Fatalf("states: %v", states)
	}
}

// The device endpoint answers 401/404/413/429 as the protocol says.
func TestDeviceAuthAndLimits(t *testing.T) {
	a := newApp(t, nil)
	_, tok, c, cl := a.addClock(a.alice, "A1B2C3D4E5FB")
	body, _ := json.Marshal(c.Request())
	if res, _ := a.rawSync("", body); res.StatusCode != 401 {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if res, _ := a.rawSync(strings.Repeat("x", 40), body); res.StatusCode != 401 {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	other := c.Request()
	other.DeviceID = "000000000000"
	b2, _ := json.Marshal(other)
	if res, _ := a.rawSync(tok, b2); res.StatusCode != 404 {
		t.Fatalf("unknown device: %d", res.StatusCode)
	}
	if res, _ := a.rawSync(tok, bytes.Repeat([]byte(" "), 9000)); res.StatusCode != 413 {
		t.Fatalf("9000 bytes: %d", res.StatusCode)
	}
	// a chatty device: six calls at once are fine, the seventh waits
	for i := 0; i < 6; i++ {
		if code := a.sync(cl, c); code != 200 {
			t.Fatalf("call %d: %d", i, code)
		}
	}
	res, _ := a.rawSync(tok, body)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
		t.Fatalf("seventh call: %d retry-after %q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	// guessing tokens from one address is stopped
	for i := 0; i < failMax; i++ {
		a.rawSync("guess", body)
	}
	if res, _ := a.rawSync(tok, body); res.StatusCode != 429 {
		t.Fatalf("after %d wrong tokens: %d", failMax, res.StatusCode)
	}
}

func TestLimitsRefill(t *testing.T) {
	l := newLimits()
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < bucketSize; i++ {
		if l.deviceWait(1) != 0 {
			t.Fatalf("call %d waited", i)
		}
	}
	if w := l.deviceWait(1); w <= 0 || w > bucketStep {
		t.Fatalf("wait %v", w)
	}
	now = now.Add(bucketStep)
	if l.deviceWait(1) != 0 {
		t.Fatal("no refill")
	}
}

func TestNewTokenAndOffline(t *testing.T) {
	a := newApp(t, nil)
	id, old, c, cl := a.addClock(a.alice, "A1B2C3D4E5FC")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.sync(cl, c)
	m := a.ok(a.alice, "POST", path+"/token", nil)
	tok := m["token"].(string)
	if tok == old {
		t.Fatal("same token")
	}
	body, _ := json.Marshal(c.Request())
	if res, _ := a.rawSync(old, body); res.StatusCode != 401 {
		t.Fatalf("old token still works: %d", res.StatusCode)
	}
	if res, _ := a.rawSync(tok, body); res.StatusCode != 200 {
		t.Fatalf("new token: %d", res.StatusCode)
	}
	// not seen for three intervals: offline
	ctx := context.Background()
	_, _ = a.st.DB.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339Nano), id)
	a.s.checkOffline(ctx)
	if d := a.ok(a.alice, "GET", path, nil)["device"].(map[string]any); d["state"] != "offline" {
		t.Fatalf("state: %v", d["state"])
	}
	a.s.limits = newLimits()
	if res, _ := a.rawSync(tok, body); res.StatusCode != 200 {
		t.Fatalf("back: %d", res.StatusCode)
	}
	_, _, l := a.call(a.alice, "GET", path+"/log", nil)
	if !strings.Contains(fmt.Sprint(l), "offline") || !strings.Contains(fmt.Sprint(l), "token") {
		t.Fatalf("log: %v", l)
	}
}

// Everything is per person: Bob cannot see, change or command Alice's clock.
func TestPerPerson(t *testing.T) {
	a := newApp(t, nil)
	id, _, _, _ := a.addClock(a.alice, "A1B2C3D4E5FD")
	path := fmt.Sprintf("/api/v1/devices/%d", id)
	a.expect(404, a.bob, "GET", path, nil)
	a.expect(404, a.bob, "PUT", path, map[string]any{"name": "Mine now"})
	a.expect(404, a.bob, "PUT", path+"/settings", map[string]any{"values": map[string]any{"alarm": "on"}})
	a.expect(404, a.bob, "POST", path+"/commands", map[string]any{"name": "sync_time"})
	a.expect(404, a.bob, "GET", path+"/log", nil)
	a.expect(404, a.bob, "GET", path+"/commands", nil)
	a.expect(404, a.bob, "GET", path+"/samples", nil)
	a.expect(404, a.bob, "POST", path+"/token", nil)
	a.expect(404, a.bob, "DELETE", path, nil)
	if _, _, l := a.call(a.bob, "GET", "/api/v1/devices", nil); len(l) != 0 {
		t.Fatalf("bob sees %v", l)
	}
	// the same MAC cannot be added twice on one box
	a.expect(409, a.bob, "POST", "/api/v1/devices", map[string]any{"kind": "ntp-clock", "name": "Copy", "hw_id": "A1B2C3D4E5FD"})
	a.expect(400, a.bob, "POST", "/api/v1/devices", map[string]any{"kind": "ntp-clock", "name": "Bad", "hw_id": "12345"})
	a.expect(204, a.alice, "DELETE", path, nil)
}

func TestSettingsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"notify_offline": false, "device_url": "https://bob.example.com"})
	if m := a.ok(a.alice, "GET", "/api/v1/settings", nil); m["notify_offline"] != true || m["device_url_effective"] != "https://devices.test" {
		t.Fatalf("alice's settings: %v", m)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"device_url": "http://plain.example.com"})
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"device_url": "https://clock.example.com"})
	m := a.expect(201, a.alice, "POST", "/api/v1/devices", map[string]any{"name": "C", "hw_id": "0A0B0C0D0E0F"})
	if s := m["setup"].([]any); s[0] != "cfg set server_url https://clock.example.com" {
		t.Fatalf("setup: %v", s)
	}
}

func TestExportImportIsIdempotent(t *testing.T) {
	a := newApp(t, nil)
	id, tok, c, cl := a.addClock(a.alice, "A1B2C3D4E5FE")
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/devices/%d/settings", id), map[string]any{"values": map[string]any{"alarm": "on", "pass": "hidden-pass"}})
	a.sync(cl, c)
	code, raw := exportRaw(a, a.alice)
	if code != 200 || strings.Contains(string(raw), "hidden-pass") || !strings.Contains(string(raw), "A1B2C3D4E5FE") {
		t.Fatalf("export: %d %s", code, raw)
	}
	// on another box (Bob's account here, after Alice removes it) the clock keeps its token
	a.expect(204, a.alice, "DELETE", fmt.Sprintf("/api/v1/devices/%d", id), nil)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	first := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(first, "devices") != 1 {
		t.Fatalf("first import: %v", first)
	}
	second := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(second, "devices") != 0 || num(second, "already") != 1 {
		t.Fatalf("second import: %v", second)
	}
	a.s.limits = newLimits()
	body, _ := json.Marshal(c.Request())
	if res, _ := a.rawSync(tok, body); res.StatusCode != 200 {
		t.Fatalf("the moved clock's token: %d", res.StatusCode)
	}
}

func exportRaw(a *app, tok string) (int, []byte) {
	req, _ := http.NewRequest("GET", a.url+"/api/v1/export.json", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func TestPosixTZ(t *testing.T) {
	tz, err := posixTZ("Europe/Sarajevo")
	if err != nil {
		t.Skip("no zoneinfo on this machine:", err)
	}
	if tz != "CET-1CEST,M3.5.0,M10.5.0/3" {
		t.Fatalf("Europe/Sarajevo → %q", tz)
	}
	if _, err := posixTZ("../../etc/passwd"); err == nil {
		t.Fatal("path accepted")
	}
}

// The demo clocks check in through the same code as real ones.
func TestDemoSeeds(t *testing.T) {
	a := newApp(t, func(c *config.Config) { c.DemoData = true; c.AdminEmail = ""; c.AuthMode = "local" })
	if err := a.s.seedDemo(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	_, _, l := a.call(a.alice, "GET", "/api/v1/devices", nil)
	if len(l) != 3 {
		t.Fatalf("demo devices: %d", len(l))
	}
	states := map[string]string{}
	for _, x := range l {
		m := x.(map[string]any)
		states[m["name"].(string)] = m["state"].(string)
	}
	if states["Bedroom clock"] != "online" || states["Office clock"] != "offline" {
		t.Fatalf("states: %v", states)
	}
}
