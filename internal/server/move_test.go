package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/config"
)

// moveLinks starts Devices behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@.
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "devices:read", "write": "devices:write"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"devices","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func (a *app) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (a *app) moveImport(tok, path string, data []byte) map[string]any {
	a.t.Helper()
	code, out := a.raw(tok, "POST", path, "application/json", data)
	if code != 200 {
		a.t.Fatalf("POST %s → %d %s", path, code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/devices", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/devices", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 1 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's clock to Bob: the import adds once and nothing of one
// person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	a.expect(201, a.alice, "POST", "/api/v1/devices", map[string]any{"kind": "ntp-clock", "name": "Lake clock", "hw_id": "112233445566"})
	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "Lake clock") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "Lake clock") {
		t.Fatalf("bob's export holds alice's clock: %s", bob)
	}
	own := a.moveImport("pcx_alice_import", "/api/v1/import", export)
	if num(own, "devices") != 0 || num(own, "already") != 1 {
		t.Fatalf("alice's own file back: %v", own)
	}
	// a move to another box: the device is not here yet there
	a.expect(204, a.alice, "DELETE", "/api/v1/devices/1", nil)
	first := a.moveImport("pcx_bob_import", "/api/v1/import", export)
	if num(first, "devices") != 1 {
		t.Fatalf("first import: %v", first)
	}
	second := a.moveImport("pcx_bob_import", "/api/v1/import", export)
	if num(second, "devices") != 0 {
		t.Fatalf("second import added: %v", second)
	}
	if _, _, l := a.call(a.bob, "GET", "/api/v1/devices", nil); len(l) != 1 {
		t.Fatalf("bob's devices: %v", l)
	}
	if _, _, l := a.call(a.alice, "GET", "/api/v1/devices", nil); len(l) != 0 {
		t.Fatalf("alice's devices: %v", l)
	}
}

// Link scopes: read sees, write changes settings and everyday commands,
// neither restarts or erases a device.
func TestLinkScopes(t *testing.T) {
	a := moveLinks(t)
	m := a.expect(201, a.bob, "POST", "/api/v1/devices", map[string]any{"kind": "ntp-clock", "name": "Bob clock", "hw_id": "665544332211"})
	id := int64(num(m["device"].(map[string]any), "id"))
	p := fmt.Sprintf("/api/v1/devices/%d", id)
	read, write := "pcx_bob_read", "pcx_bob_write"
	for _, c := range []struct {
		tok, method, path string
		body              string
		want              int
	}{
		{read, "GET", "/api/v1/devices", "", 200},
		{read, "GET", p, "", 200},
		{read, "GET", p + "/log", "", 200},
		{read, "PUT", p + "/settings", `{"values":{"alarm":"on"}}`, 403},
		{read, "POST", "/api/v1/devices", `{"hw_id":"010203040506"}`, 403},
		{write, "PUT", p + "/settings", `{"values":{"alarm":"on"}}`, 200},
		{write, "POST", p + "/commands", `{"name":"alarm_stop"}`, 201},
		{write, "POST", p + "/commands", `{"name":"reboot","confirm":true}`, 403},
		{write, "POST", p + "/token", "", 403},
		{write, "DELETE", p, "", 403},
		{write, "GET", "/api/v1/settings", "", 403},
		{"pcx_alice_read", "GET", p, "", 404},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte(c.body)); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, _, l := a.call(a.bob, "GET", p+"/log", nil)
	if !strings.Contains(fmt.Sprint(l), "Pocket Move") {
		t.Fatalf("the linked app is not named in the log: %v", l)
	}
}
