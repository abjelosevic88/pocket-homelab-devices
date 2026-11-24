// Package clocksim behaves like an NTP Clock talking to the server (protocol
// version 1): it keeps a settings table with the device's defaults, builds
// the sync request, applies the answer with the same rules as the device's
// console and remembers the results of the commands it ran. Used by the
// `pocket-devices simulate` command, the demo clocks and the tests.
package clocksim

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
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
)

// Request is what the clock sends.
type Request struct {
	DeviceID       string            `json:"device_id"`
	FW             string            `json:"fw"`
	ResetReason    string            `json:"reset_reason"`
	UptimeS        int64             `json:"uptime_s"`
	HeapFree       int               `json:"heap_free"`
	Display        string            `json:"display"`
	WiFi           *WiFi             `json:"wifi,omitempty"`
	Time           *Time             `json:"time,omitempty"`
	Weather        *Weather          `json:"weather,omitempty"`
	Alarm          *Alarm            `json:"alarm,omitempty"`
	ConfigRev      int64             `json:"config_rev"`
	Config         map[string]string `json:"config"`
	ConfigErrors   map[string]string `json:"config_errors,omitempty"`
	CommandResults []Result          `json:"command_results,omitempty"`
}

// WiFi is the clock's network.
type WiFi struct {
	SSID    string `json:"ssid"`
	RSSI    int    `json:"rssi"`
	Channel int    `json:"channel"`
	IP      string `json:"ip"`
}

// Time is the clock's time sync state.
type Time struct {
	Synced       bool   `json:"synced"`
	Unix         *int64 `json:"unix,omitempty"`
	LastSyncAgeS int64  `json:"last_sync_age_s"`
}

// Weather is the outdoor temperature.
type Weather struct {
	Place string  `json:"place"`
	TempC float64 `json:"temp_c"`
}

// Alarm is the alarm state.
type Alarm struct {
	Ringing bool `json:"ringing"`
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
	Time      *struct {
		Unix int64  `json:"unix"`
		TZ   string `json:"tz"`
	} `json:"time,omitempty"`
}

// Clock is one simulated clock.
type Clock struct {
	mu        sync.Mutex
	ID        string
	FW        string
	kind      *kinds.Kind
	config    map[string]string
	rev       int64
	errors    map[string]string
	results   []Result
	booted    time.Time
	reset     string
	lastSync  time.Time
	ringing   bool
	rng       *rand.Rand
	baseTemp  float64
	baseRSSI  int
	IP        string
	PollS     int
	rebooting bool
	Log       func(string)
}

// New makes a clock with the device's defaults.
func New(id string, seed int64) *Clock {
	k, _ := kinds.Get("ntp-clock")
	c := &Clock{ID: id, FW: "0.1.0", kind: k, config: map[string]string{}, errors: map[string]string{}, booted: time.Now(), reset: "power-on",
		rng: rand.New(rand.NewSource(seed)), PollS: 30, IP: fmt.Sprintf("192.168.100.%d", 60+seed%40)}
	for _, s := range k.Settings {
		c.config[s.Key] = s.Default
	}
	c.config["lat"], c.config["lon"] = "0.000000", "0.000000"
	c.config["remote"] = "on"
	c.lastSync = time.Now().Add(-time.Duration(c.rng.Intn(1800)) * time.Second)
	c.baseTemp = 4 + c.rng.Float64()*8
	c.baseRSSI = -48 - c.rng.Intn(18)
	return c
}

// Set changes a setting as if typed at the console (no checks; for setup).
func (c *Clock) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config[key] = value
}

// Get reads a setting.
func (c *Clock) Get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config[key]
}

// Rev is the revision the clock applied last.
func (c *Clock) Rev() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rev
}

// SetRev sets the revision the clock applied last (a clock that restarts).
func (c *Clock) SetRev(rev int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rev = rev
}

// SetRinging starts or stops the alarm (for the demo).
func (c *Clock) SetRinging(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ringing = on
}

// Request builds the next sync request; the results are sent once.
func (c *Clock) Request() Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	cfg := make(map[string]string, len(c.config))
	for k, v := range c.config {
		if s, ok := c.kind.Setting(k); ok && s.Secret && v != "" {
			v = kinds.SecretMask
		}
		cfg[k] = v
	}
	unix := now.Unix()
	hours := float64(now.Hour()) + float64(now.Minute())/60
	temp := c.baseTemp + 4*math.Sin((hours-9)/24*2*math.Pi) + c.rng.Float64()*0.4
	r := Request{DeviceID: c.ID, FW: c.FW, ResetReason: c.reset, UptimeS: int64(now.Sub(c.booted).Seconds()), HeapFree: 178000 + c.rng.Intn(6000),
		Display: c.config["display"], ConfigRev: c.rev, Config: cfg,
		WiFi:  &WiFi{SSID: c.config["ssid"], RSSI: c.baseRSSI + c.rng.Intn(3) - 1, Channel: 6, IP: c.IP},
		Time:  &Time{Synced: true, Unix: &unix, LastSyncAgeS: int64(now.Sub(c.lastSync).Seconds())},
		Alarm: &Alarm{Ringing: c.ringing},
	}
	if c.config["place"] != "" {
		r.Weather = &Weather{Place: c.config["place"], TempC: math.Round(temp*10) / 10}
	}
	if len(c.errors) > 0 {
		r.ConfigErrors = c.errors
	}
	if len(c.results) > 0 {
		r.CommandResults = c.results
	}
	return r
}

// Apply takes an answer: settings with the console rules (refused ones keep
// their old value and are reported), then the commands in order.
func (c *Clock) Apply(resp Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = map[string]string{}
	c.results = nil
	if resp.PollS >= 10 && resp.PollS <= 3600 {
		c.PollS = resp.PollS
	}
	if resp.ConfigRev != c.rev {
		keys := make([]string, 0, len(resp.Config))
		for k := range resp.Config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := resp.Config[k]
			s, ok := c.kind.Setting(k)
			switch {
			case !ok:
				c.errors[k] = "unknown setting"
			case s.Locked:
				c.errors[k] = "not changed remotely"
			case s.Secret && v == kinds.SecretMask:
			default:
				n, err := s.Normalize(v)
				if err != nil {
					c.errors[k] = err.Error()
					continue
				}
				c.config[k] = n
				c.logf("cfg %s = %s", k, shown(s, n))
			}
		}
		c.rev = resp.ConfigRev
	}
	for _, cmd := range resp.Commands {
		id, _ := cmd["id"].(string)
		name, _ := cmd["name"].(string)
		res := Result{ID: id, OK: true}
		switch name {
		case "reboot", "setup_mode":
			c.logf("%s: restarting", name)
			c.booted = time.Now()
			c.reset = "software"
			continue // a restart cannot report itself
		case "sync_time":
			c.lastSync = time.Now()
		case "weather_refresh":
		case "alarm_stop":
			c.ringing = false
		case "alarm_test":
			n, _ := cmd["n"].(float64)
			if n < 0 || n > 6 || n != math.Trunc(n) {
				res.OK, res.Msg = false, "n must be 0..6"
			} else {
				res.Msg = "played sound " + strconv.Itoa(int(n))
			}
		case "cfg_reset":
			for _, s := range c.kind.Settings {
				c.config[s.Key] = s.Default
			}
			c.config["lat"], c.config["lon"] = "0.000000", "0.000000"
			c.rev = 0
			c.logf("cfg_reset: settings erased")
		default:
			res.OK, res.Msg = false, "unknown command"
		}
		c.logf("command %s %s: ok=%v %s", id, name, res.OK, res.Msg)
		c.results = append(c.results, res)
	}
}

func shown(s *kinds.Setting, v string) string {
	if s.Secret {
		return kinds.SecretMask
	}
	return v
}

func (c *Clock) logf(f string, a ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf("[%s] ", c.ID) + fmt.Sprintf(f, a...))
	}
}

// Client talks to a server over HTTP like the real clock.
type Client struct {
	URL   string // server_url, no trailing slash
	Token string
	HTTP  *http.Client
}

// Sync sends one request and applies the answer. It returns the status code
// and, for 429, how long to wait.
func (cl *Client) Sync(ctx context.Context, c *Clock) (int, time.Duration, error) {
	body, _ := json.Marshal(c.Request())
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
		return res.StatusCode, 0, fmt.Errorf("server answered %d: %s", res.StatusCode, bytes.TrimSpace(raw))
	}
	if len(raw) > 8<<10 {
		return res.StatusCode, 0, fmt.Errorf("answer is %d bytes, more than 8 KiB", len(raw))
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return res.StatusCode, 0, fmt.Errorf("answer is not JSON: %w", err)
	}
	c.Apply(resp)
	return res.StatusCode, 0, nil
}
