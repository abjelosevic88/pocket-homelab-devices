package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/sim"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// demoDev is a simulated device living inside the server (DEMO_DATA=true):
// it checks in like a real one, so every screen can be tried without hardware.
type demoDev struct {
	dev   *sim.Device
	nudge chan struct{}
	ring  bool // a clock that rings now and then until someone stops it
}

type demoSpec struct {
	kind, name, hwid, location string
	settings                   map[string]string // set here over the last weeks
	device                     map[string]string // what the device has besides its defaults
	offlineFor                 time.Duration     // > 0: stopped checking in that long ago
}

var demoHome = map[string]string{"place": "Banja Luka", "lat": "44.772186", "lon": "17.191000", "tz": "CET-1CEST,M3.5.0,M10.5.0/3"}

var demoSpecs = []demoSpec{
	{"ntp-clock", "Bedroom clock", "A1B2C3D4E5F6", "Bedroom", map[string]string{"alarm": "on", "alarm_time": "06:30", "alarm_days": "weekdays", "brightness": "40", "bright_auto": "off", "chime": "off"}, demoHome, 0},
	{"ntp-clock", "Kitchen clock", "3C71BF0A9E12", "Kitchen", map[string]string{"alarm": "off"}, map[string]string{"display": "lcd", "place": "Banja Luka", "lat": "44.772186", "lon": "17.191000", "tz": "CET-1CEST,M3.5.0,M10.5.0/3"}, 0},
	{"ntp-clock", "Office clock", "7CDFA1B3C4D5", "Office", map[string]string{"show_seconds": "off"}, map[string]string{"place": "Sarajevo", "lat": "43.856430", "lon": "18.413029"}, 52 * time.Hour},
	{"climate-sensor", "Living room climate", "84F703A1C2E0", "Living room", map[string]string{"alert_temp": "on", "temp_high": "26.0", "temp_low": "18.0"}, nil, 0},
	{"climate-sensor", "Bathroom climate", "84F703A1C2E4", "Bathroom", map[string]string{"alert_hum": "on", "hum_high": "45"}, nil, 0},
	{"air-quality", "Office air", "F412FA55B0A8", "Office", map[string]string{"co2_warn": "900", "ring_brightness": "25"}, nil, 0},
	{"contact-sensor", "Front door", "60550F1A2B3C", "Hallway", map[string]string{"open_warn_min": "5"}, nil, 0},
	{"motion-sensor", "Hallway motion", "60550F1A2B40", "Hallway", map[string]string{"hold_s": "120"}, nil, 0},
	{"button", "Desk button", "34851870A1B2", "Office", map[string]string{"single_name": "Desk lamp", "double_name": "Focus timer", "long_name": "Do not disturb"}, nil, 0},
	{"smart-plug", "Coffee machine", "E8DB84C1D2E3", "Kitchen", map[string]string{"auto_off_min": "90"}, nil, 0},
	{"leak-sensor", "Under the sink", "48E72994B1C0", "Kitchen", map[string]string{"alert_freeze": "off"}, nil, 0},
}

// seedDemo adds one of every kind of device to the owner's account, with
// two weeks of readings and a few days of events; all but one check in live.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	ctx = context.WithoutCancel(ctx)
	key := fmt.Sprintf("seeded:%d:demo2", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	if _, _, old := s.store.CacheGet(ctx, fmt.Sprintf("seeded:%d:demo", owner)); old {
		return nil // seeded by 0.1: keep what is there
	}
	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	base := firstNonEmpty(s.cfg.BaseURL, "https://devices.example.com")
	for i, sp := range demoSpecs {
		k, _ := kinds.Get(sp.kind)
		live := sp.offlineFor == 0
		d := &store.Device{Kind: k.ID, Name: sp.name, HWID: sp.hwid, Location: sp.location, TokenHash: store.HashToken(store.RandomToken(20)),
			TokenHint: "demo00", PollS: k.Poll.Default, NotifyOffline: true, Demo: live}
		if err := s.store.CreateDevice(ctx, owner, d, "demo"); err != nil {
			return err
		}
		dev := newDemoDev(k.ID, sp.hwid, int64(i+1), base)
		for key, v := range sp.device {
			dev.Set(key, v)
		}
		if _, _, err := s.store.SetDesired(ctx, owner, d.ID, k, sp.settings, "Aleksandar", "setting"); err != nil {
			return err
		}
		// the first check-in takes the revision, the second confirms it
		for range 2 {
			req := dev.Request()
			if out, err := s.syncDevice(ctx, d, mustMarshal(req), parseReq(req), dev.IP); err == nil {
				dev.Apply(toResp(out))
			}
			d, _ = s.store.DeviceByID(ctx, d.ID)
		}
		// two weeks of readings
		end := now
		if !live {
			end = now.Add(-sp.offlineFor)
		}
		for t := now.Add(-14 * 24 * time.Hour); t.Before(end); t = t.Add(5 * time.Minute) {
			values := k.Values(roundTrip(dev.StatusAt(t)), d.Reported)
			values["rssi"] = math.Round(-52 - float64(i)*2 + 2*math.Sin(float64(t.Unix())/5400+float64(i)) + rng.Float64()*0.8)
			for _, r := range k.Readings {
				if f, ok := values[r.Key].(float64); ok && r.Chart {
					if err := s.store.AddReading(ctx, owner, d.ID, r.Key, t, f); err != nil {
						return err
					}
				}
			}
		}
		s.seedDemoEvents(ctx, owner, d.ID, k, rng, now)
		if !live {
			seen := end.UTC().Format(time.RFC3339Nano)
			_, _ = s.store.DB.ExecContext(ctx, `UPDATE devices SET last_seen_at = ?, online = 0, first_seen_at = ? WHERE id = ?`, seen, now.Add(-40*24*time.Hour).UTC().Format(time.RFC3339Nano), d.ID)
			_ = s.store.AddLog(ctx, owner, d.ID, store.LogEntry{Actor: "device", Action: "offline", Detail: "stopped checking in"})
			// changed while it was away: waits for its return
			_, _, _ = s.store.SetDesired(ctx, owner, d.ID, k, map[string]string{"brightness": "70"}, "Aleksandar", "setting")
			continue
		}
		if sp.name == "Kitchen clock" {
			for _, name := range []string{"sync_time", "weather_refresh"} {
				_, _ = s.store.QueueCommand(ctx, owner, d.ID, name, nil, "Aleksandar")
			}
		}
		s.demoMu.Lock()
		s.demo[d.ID] = &demoDev{dev: dev, nudge: make(chan struct{}, 1), ring: sp.name == "Bedroom clock"}
		s.demoMu.Unlock()
	}
	return s.store.CachePut(ctx, key, "1")
}

// seedDemoEvents adds three days of presses, doors and motion.
func (s *Server) seedDemoEvents(ctx context.Context, owner, deviceID int64, k *kinds.Kind, rng *rand.Rand, now time.Time) {
	if len(k.Events) == 0 {
		return
	}
	open, motion, leaked := false, false, false
	for t := now.Add(-72 * time.Hour); t.Before(now.Add(-10 * time.Minute)); t = t.Add(time.Duration(10+rng.Intn(50)) * time.Minute) {
		h := t.Hour()
		if h < 6 || h > 23 {
			continue
		}
		switch k.ID {
		case "button":
			if rng.Float64() < 0.25 {
				a := []string{"single", "single", "single", "double", "long"}[rng.Intn(5)]
				_ = s.store.AddEvent(ctx, owner, deviceID, "press", map[string]any{"action": a}, t)
			}
		case "contact-sensor":
			if rng.Float64() < 0.3 {
				_ = s.store.AddEvent(ctx, owner, deviceID, "opened", nil, t)
				_ = s.store.AddEvent(ctx, owner, deviceID, "closed", nil, t.Add(time.Duration(10+rng.Intn(120))*time.Second))
				open = !open
			}
		case "motion-sensor":
			if rng.Float64() < 0.5 {
				_ = s.store.AddEvent(ctx, owner, deviceID, "motion", nil, t)
				_ = s.store.AddEvent(ctx, owner, deviceID, "quiet", nil, t.Add(time.Duration(2+rng.Intn(10))*time.Minute))
				motion = !motion
			}
		case "leak-sensor":
			if !leaked && t.After(now.Add(-48*time.Hour)) {
				leaked = true
				_ = s.store.AddEvent(ctx, owner, deviceID, "leak", nil, t)
				_ = s.store.AddEvent(ctx, owner, deviceID, "dry", nil, t.Add(25*time.Minute))
			}
		case "smart-plug":
			if (h == 7 || h == 14) && rng.Float64() < 0.6 {
				_ = s.store.AddEvent(ctx, owner, deviceID, "switched", map[string]any{"state": "on", "by": "button"}, t)
				_ = s.store.AddEvent(ctx, owner, deviceID, "switched", map[string]any{"state": "off", "by": "auto_off"}, t.Add(90*time.Minute))
			}
		}
	}
}

func newDemoDev(kindID, hwid string, seed int64, url string) *sim.Device {
	d := sim.New(kindID, hwid, seed)
	d.Set("ssid", "Apolyx")
	d.Set("pass", "correct-horse")
	d.Set("server_url", url)
	d.Set("device_token", "demo")
	return d
}

func roundTrip(m map[string]any) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(mustMarshal(m), &out)
	return out
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func parseReq(r sim.Request) syncReq {
	var out syncReq
	_ = json.Unmarshal(mustMarshal(r), &out)
	return out
}

func toResp(m map[string]any) sim.Response {
	var r sim.Response
	_ = json.Unmarshal(mustMarshal(m), &r)
	return r
}

// demoLoop lets the simulated devices live: they check in every poll
// interval, a few seconds after a change, and (like a sleeping button or a
// door sensor) right away when something happens.
func (s *Server) demoLoop(ctx context.Context) {
	if ds, err := s.store.DemoDevices(ctx); err == nil {
		for i, d := range ds {
			s.demoMu.Lock()
			if _, ok := s.demo[d.ID]; !ok {
				dev := newDemoDev(d.Kind, d.HWID, int64(i+1), firstNonEmpty(s.cfg.BaseURL, "https://devices.example.com"))
				for key, v := range d.Reported {
					if v != kinds.SecretMask {
						dev.Set(key, v)
					}
				}
				dev.SetRev(d.AppliedRev)
				s.demo[d.ID] = &demoDev{dev: dev, nudge: make(chan struct{}, 1), ring: d.Name == "Bedroom clock"}
			}
			s.demoMu.Unlock()
		}
	}
	s.demoMu.Lock()
	devs := map[int64]*demoDev{}
	for id, d := range s.demo {
		devs[id] = d
	}
	s.demoMu.Unlock()
	for id, d := range devs {
		go s.runDemoDev(ctx, id, d)
	}
}

func (s *Server) runDemoDev(ctx context.Context, id int64, dd *demoDev) {
	ringAt := time.Now().Add(2 * time.Minute)
	last := time.Now()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		due := false
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			dd.dev.Tick(time.Now())
			due = dd.dev.HasPending() || time.Since(last) >= time.Duration(max(dd.dev.PollS, 10))*time.Second
		case <-dd.nudge:
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			// a sleeping device only hears the change at its next check-in
			due = dd.dev.Kind.Power != "battery"
		}
		if !due {
			continue
		}
		s.demoMu.Lock()
		_, alive := s.demo[id]
		s.demoMu.Unlock()
		if !alive {
			return
		}
		d, err := s.store.DeviceByID(ctx, id)
		if err != nil {
			return
		}
		if dd.ring && time.Now().After(ringAt) {
			dd.dev.SetRinging(true)
			ringAt = time.Now().Add(6 * time.Hour)
		}
		req := dd.dev.Request()
		out, err := s.syncDevice(ctx, d, mustMarshal(req), parseReq(req), dd.dev.IP)
		last = time.Now()
		if err != nil {
			s.log.Debug("demo device", "err", err)
			continue
		}
		dd.dev.Apply(toResp(out))
	}
}

// demoNudge makes a simulated device check in soon after a change.
func (s *Server) demoNudge(id int64) {
	s.demoMu.Lock()
	dd, ok := s.demo[id]
	s.demoMu.Unlock()
	if !ok {
		return
	}
	select {
	case dd.nudge <- struct{}{}:
	default:
	}
}
