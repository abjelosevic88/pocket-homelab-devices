// Package sim behaves like any device of the kinds Pocket Devices knows,
// talking the sync protocol: it keeps a settings table with the kind's
// defaults, makes up plausible readings (a temperature that follows the day,
// CO2 that rises in the evening, a plug's power use), tells events (button
// presses, a door opening, motion), applies answers with the same rules as
// the device's console and answers commands. Used by `pocket-devices
// simulate`, the demo devices and the tests.
package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
)

// Request is what a device sends. Status holds the kind's own report
// fields ("climate", "contact"…) and is merged into the JSON.
type Request struct {
	DeviceID       string            `json:"device_id"`
	FW             string            `json:"fw"`
	ResetReason    string            `json:"reset_reason"`
	UptimeS        int64             `json:"uptime_s"`
	HeapFree       int               `json:"heap_free"`
	WiFi           *WiFi             `json:"wifi,omitempty"`
	ConfigRev      int64             `json:"config_rev"`
	Config         map[string]string `json:"config"`
	ConfigErrors   map[string]string `json:"config_errors,omitempty"`
	CommandResults []Result          `json:"command_results,omitempty"`
	Events         []map[string]any  `json:"events,omitempty"`
	Status         map[string]any    `json:"-"`
}

// MarshalJSON puts the kind's own fields next to the common ones.
func (r Request) MarshalJSON() ([]byte, error) {
	type plain Request
	b, err := json.Marshal(plain(r))
	if err != nil || len(r.Status) == 0 {
		return b, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range r.Status {
		if _, taken := m[k]; !taken {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// WiFi is the device's network.
type WiFi struct {
	SSID    string `json:"ssid"`
	RSSI    int    `json:"rssi"`
	Channel int    `json:"channel"`
	IP      string `json:"ip"`
}

// Result is the outcome of one command.
type Result struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

// Response is the server's answer.
type Response struct {
	ConfigRev int64             `json:"config_rev"`
	Config    map[string]string `json:"config,omitempty"`
	Commands  []map[string]any  `json:"commands,omitempty"`
	PollS     int               `json:"poll_s,omitempty"`
}

// Device is one simulated device.
type Device struct {
	mu      sync.Mutex
	ID      string
	FW      string
	Kind    *kinds.Kind
	config  map[string]string
	rev     int64
	errors  map[string]string
	results []Result
	pending []map[string]any // events not yet delivered
	sent    int              // events in the last request
	evSeq   int
	booted  time.Time
	reset   string
	rng     *rand.Rand
	base    float64 // a per-device offset so two sensors differ
	rssi    int
	IP      string
	PollS   int
	Log     func(string)

	// state
	lastSync time.Time
	ringing  bool
	on       bool
	energy   float64
	open     bool
	openedAt time.Time
	motion   bool
	wet      bool
	lastPush string
	load     float64 // a plug's fixed load in watts (0 = made up)
}

// SetLoad plugs something of this many watts into a plug.
func (d *Device) SetLoad(w float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load = w
}

// New makes a device of a kind with its defaults.
func New(kindID, id string, seed int64) *Device {
	k, ok := kinds.Get(kindID)
	if !ok {
		panic("sim: unknown kind " + kindID)
	}
	rng := rand.New(rand.NewSource(seed))
	d := &Device{ID: id, FW: "0.1.0", Kind: k, config: map[string]string{}, errors: map[string]string{}, booted: time.Now(), reset: "power-on",
		rng: rng, PollS: k.Poll.Default, IP: fmt.Sprintf("192.168.100.%d", 60+seed%40), base: rng.Float64()*2 - 1, rssi: -48 - rng.Intn(18)}
	for _, s := range k.Settings {
		v := s.Default
		if n, err := s.Normalize(v); err == nil {
			v = n
		}
		d.config[s.Key] = v
	}
	d.config["remote"] = "on"
	d.lastSync = time.Now().Add(-time.Duration(rng.Intn(1800)) * time.Second)
	d.on = true
	d.energy = 3 + rng.Float64()*20
	return d
}

// Set changes a setting as if typed at the console (no checks; for setup).
func (d *Device) Set(key, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config[key] = value
}

// Get reads a setting.
func (d *Device) Get(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.config[key]
}

// Rev is the revision the device applied last.
func (d *Device) Rev() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rev
}

// SetRev sets the applied revision (a device that restarts).
func (d *Device) SetRev(rev int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rev = rev
}

// SetRinging starts or stops a clock's alarm.
func (d *Device) SetRinging(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ringing = on
}

// Emit queues an event for the next request (a press, a door opening).
func (d *Device) Emit(typ string, fields map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.emit(typ, fields, time.Now())
}

func (d *Device) emit(typ string, fields map[string]any, at time.Time) {
	d.evSeq++
	e := map[string]any{"id": fmt.Sprintf("e-%d-%d", d.booted.Unix()%100000, d.evSeq), "type": typ, "at": at.Unix()}
	for k, v := range fields {
		e[k] = v
	}
	d.pending = append(d.pending, e)
	if len(d.pending) > 20 {
		d.pending = d.pending[len(d.pending)-20:]
	}
}

// Press simulates a button press (single, double or long).
func (d *Device) Press(action string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastPush = action
	d.emit("press", map[string]any{"action": action}, time.Now())
}

// SetOpen opens or closes a contact sensor.
func (d *Device) SetOpen(open bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setOpen(open, time.Now())
}

func (d *Device) setOpen(open bool, at time.Time) {
	if open == d.open {
		return
	}
	d.open = open
	if open {
		d.openedAt = at
		d.emit("opened", nil, at)
	} else {
		d.emit("closed", nil, at)
	}
}

// SetWet puts a leak sensor in water (or takes it out).
func (d *Device) SetWet(wet bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if wet == d.wet {
		return
	}
	d.wet = wet
	if wet {
		d.emit("leak", nil, time.Now())
	} else {
		d.emit("dry", nil, time.Now())
	}
}

// Tick lets time pass: random presses, doors, motion (for the demo).
func (d *Device) Tick(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.Kind.ID {
	case "button":
		if d.rng.Float64() < 0.08 {
			a := []string{"single", "single", "single", "double", "long"}[d.rng.Intn(5)]
			d.lastPush = a
			d.emit("press", map[string]any{"action": a}, now)
		}
	case "contact-sensor":
		if d.rng.Float64() < 0.1 {
			d.setOpen(!d.open, now)
		}
	case "motion-sensor":
		if d.rng.Float64() < 0.15 {
			d.motion = !d.motion
			if d.motion {
				d.emit("motion", nil, now)
			} else {
				d.emit("quiet", nil, now)
			}
		}
	}
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// daily is a value that follows the time of day (peak at 15:00).
func daily(t time.Time, mid, amp float64) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	return mid + amp*math.Sin((h-9)/24*2*math.Pi)
}

// StatusAt makes up the kind's own report fields for a past moment (demo history).
func (d *Device) StatusAt(t time.Time) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status(t)
}

// HasPending says whether events wait to be sent (a sleeping device wakes up for them).
func (d *Device) HasPending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending) > 0
}

// Status makes up the kind's own report fields for a moment in time.
func (d *Device) status(now time.Time) map[string]any {
	f := func(v float64) float64 { return v + d.rng.Float64()*0.4 - 0.2 }
	battery := func() map[string]any {
		days := now.Sub(d.booted).Hours() / 24
		return map[string]any{"pct": math.Max(5, round(92-days*0.05+d.base*4, 0))}
	}
	switch d.Kind.ID {
	case "ntp-clock":
		st := map[string]any{"display": d.config["display"],
			"time":  map[string]any{"synced": true, "unix": now.Unix(), "last_sync_age_s": int64(now.Sub(d.lastSync).Seconds())},
			"alarm": map[string]any{"ringing": d.ringing}}
		if d.config["place"] != "" {
			st["weather"] = map[string]any{"place": d.config["place"], "temp_c": round(f(daily(now, 8+d.base*3, 4)), 1)}
		}
		return st
	case "climate-sensor":
		off, _ := strconv.ParseFloat(d.config["temp_offset"], 64)
		return map[string]any{"climate": map[string]any{"temp_c": round(f(daily(now, 21.5+d.base, 1.2))+off, 1),
			"humidity": round(f(daily(now, 52+d.base*8, -6)), 0), "pressure_hpa": round(1013+d.base*6+3*math.Sin(float64(now.Unix())/86400), 0)},
			"battery": battery()}
	case "air-quality":
		h := float64(now.Hour())
		co2 := 520 + 650*math.Max(0, math.Sin((h-16)/10*math.Pi)) + 60*math.Sin(float64(now.Unix())/2700) + d.rng.Float64()*15
		slow := float64(now.Unix()) / 3600 // hours: smooth drifts like real air
		pm := 6 + d.base*2 + 3*math.Sin(slow/5) + 2*math.Sin(slow/1.7) + d.rng.Float64()*0.6
		voc := 100 + 25*math.Sin(slow/3.1) + 12*math.Max(0, math.Sin((h-17)/8*math.Pi)) + d.rng.Float64()*3
		return map[string]any{"air": map[string]any{"co2_ppm": round(co2, 0), "pm25": round(math.Max(1, pm), 1), "voc_index": round(voc, 0)},
			"climate": map[string]any{"temp_c": round(f(daily(now, 22, 1)), 1), "humidity": round(f(daily(now, 45, -5)), 0)}}
	case "button":
		return map[string]any{"button": map[string]any{"last_action": d.lastPush}, "battery": battery()}
	case "contact-sensor":
		openMin := 0.0
		if d.open {
			openMin = math.Floor(now.Sub(d.openedAt).Minutes())
		}
		return map[string]any{"contact": map[string]any{"open": d.open, "open_min": openMin}, "battery": battery()}
	case "motion-sensor":
		lux := math.Max(0, daily(now, 120, 220)+d.rng.Float64()*15)
		if now.Hour() < 6 || now.Hour() > 21 {
			lux = d.rng.Float64() * 3
		}
		return map[string]any{"motion": map[string]any{"active": d.motion}, "light": map[string]any{"lux": round(lux, 0)}, "battery": battery()}
	case "smart-plug":
		w := 0.0
		if d.on {
			w = 60 + 40*math.Sin(float64(now.Unix())/900) + d.rng.Float64()*8
			if now.Hour() >= 18 && now.Hour() < 23 {
				w += 180
			}
			if d.load > 0 {
				w = d.load
			}
		}
		return map[string]any{"relay": map[string]any{"on": d.on}, "meter": map[string]any{"power_w": round(w, 0), "energy_kwh": round(d.energy, 2), "voltage": round(229+d.rng.Float64()*4, 0)}}
	case "leak-sensor":
		return map[string]any{"leak": map[string]any{"wet": d.wet}, "climate": map[string]any{"temp_c": round(f(daily(now, 14+d.base*2, 1.5)), 1)}, "battery": battery()}
	}
	return map[string]any{}
}

// Request builds the next sync request; results and events are sent until
// an answer arrives.
func (d *Device) Request() Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	cfg := make(map[string]string, len(d.config))
	for k, v := range d.config {
		if s, ok := d.Kind.Setting(k); ok && s.Secret && v != "" {
			v = kinds.SecretMask
		}
		cfg[k] = v
	}
	r := Request{DeviceID: d.ID, FW: d.FW, ResetReason: d.reset, UptimeS: int64(now.Sub(d.booted).Seconds()), HeapFree: 178000 + d.rng.Intn(6000),
		ConfigRev: d.rev, Config: cfg, WiFi: &WiFi{SSID: d.config["ssid"], RSSI: d.rssi + d.rng.Intn(3) - 1, Channel: 6, IP: d.IP},
		Status: d.status(now)}
	if len(d.errors) > 0 {
		r.ConfigErrors = d.errors
	}
	if len(d.results) > 0 {
		r.CommandResults = d.results
	}
	if len(d.pending) > 0 {
		r.Events = append([]map[string]any(nil), d.pending...)
	}
	d.sent = len(d.pending)
	return r
}

// Apply takes an answer: the events of the last request arrived, settings
// are applied with the console rules (refused ones keep their old value and
// are reported), then the commands run in order.
func (d *Device) Apply(resp Response) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sent > 0 && d.sent <= len(d.pending) {
		d.pending = d.pending[d.sent:]
	}
	d.sent = 0
	d.errors = map[string]string{}
	d.results = nil
	if resp.PollS >= d.Kind.Poll.Min && resp.PollS <= d.Kind.Poll.Max {
		d.PollS = resp.PollS
	}
	if resp.ConfigRev != d.rev {
		keys := make([]string, 0, len(resp.Config))
		for k := range resp.Config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := resp.Config[k]
			s, ok := d.Kind.Setting(k)
			switch {
			case !ok:
				d.errors[k] = "unknown setting"
			case s.Locked:
				d.errors[k] = "not changed remotely"
			case s.Secret && v == kinds.SecretMask:
			default:
				n, err := s.Normalize(v)
				if err != nil {
					d.errors[k] = err.Error()
					continue
				}
				d.config[k] = n
				d.logf("cfg %s = %s", k, shown(s, n))
			}
		}
		d.rev = resp.ConfigRev
	}
	for _, cmd := range resp.Commands {
		id, _ := cmd["id"].(string)
		name, _ := cmd["name"].(string)
		res := Result{ID: id, OK: true}
		if _, known := d.Kind.Command(name); !known {
			res.OK, res.Msg = false, "unknown command"
			d.results = append(d.results, res)
			continue
		}
		switch name {
		case "reboot", "setup_mode":
			d.logf("%s: restarting", name)
			d.booted = time.Now()
			d.reset = "software"
			continue // a restart cannot report itself
		case "sync_time":
			d.lastSync = time.Now()
		case "alarm_stop":
			d.ringing = false
		case "alarm_test":
			n, _ := cmd["n"].(float64)
			if n < 0 || n > 6 || n != math.Trunc(n) {
				res.OK, res.Msg = false, "n must be 0..6"
			} else {
				res.Msg = "played sound " + strconv.Itoa(int(n))
			}
		case "turn_on", "turn_off", "toggle":
			was := d.on
			d.on = name == "turn_on" || (name == "toggle" && !d.on)
			if was != d.on {
				d.emit("switched", map[string]any{"state": map[bool]string{true: "on", false: "off"}[d.on], "by": "server"}, time.Now())
			}
		case "reset_energy":
			d.energy = 0
		case "silence":
			res.Msg = "buzzer off"
		case "calibrate_co2":
			res.Msg = "calibrated to 420 ppm"
		case "cfg_reset":
			for _, s := range d.Kind.Settings {
				v := s.Default
				if n, err := s.Normalize(v); err == nil {
					v = n
				}
				d.config[s.Key] = v
			}
			d.rev = 0
			d.logf("cfg_reset: settings erased")
		}
		d.logf("command %s %s: ok=%v %s", id, name, res.OK, res.Msg)
		d.results = append(d.results, res)
	}
}

func shown(s *kinds.Setting, v string) string {
	if s.Secret {
		return kinds.SecretMask
	}
	return v
}

func (d *Device) logf(f string, a ...any) {
	if d.Log != nil {
		d.Log(fmt.Sprintf("[%s] ", d.ID) + fmt.Sprintf(f, a...))
	}
}

// Client talks to a server over HTTP like the real device.
type Client struct {
	URL   string // server_url, no trailing slash
	Token string
	HTTP  *http.Client
}

// Sync sends one request and applies the answer. It returns the status code
// and, for 429, how long to wait.
func (cl *Client) Sync(ctx context.Context, d *Device) (int, time.Duration, error) {
	body, err := json.Marshal(d.Request())
	if err != nil {
		return 0, 0, err
	}
	if len(body) > 8<<10 {
		return 0, 0, fmt.Errorf("request is %d bytes, more than 8 KiB", len(body))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.URL+"/api/v1/device/sync", bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cl.Token)
	hc := cl.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := hc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		return res.StatusCode, time.Duration(max(wait, 1)) * time.Second, nil
	default:
		return res.StatusCode, 0, fmt.Errorf("server answered %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(raw) > 8<<10 {
		return res.StatusCode, 0, fmt.Errorf("answer is %d bytes, more than 8 KiB", len(raw))
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return res.StatusCode, 0, fmt.Errorf("answer is not JSON: %w", err)
	}
	d.Apply(resp)
	return res.StatusCode, 0, nil
}
