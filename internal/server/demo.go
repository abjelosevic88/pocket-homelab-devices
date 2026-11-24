package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/clocksim"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
	"github.com/abjelosevic88/pocket-homelab-devices/internal/store"
)

// demoClock is a simulated clock living inside the server (DEMO_DATA=true):
// it checks in like a real one, so every screen can be tried without hardware.
type demoClock struct {
	clock *clocksim.Clock
	nudge chan struct{}
	ring  bool // rings now and then until someone stops it
}

type demoSpec struct {
	name, hwid, location, place, lat, lon string
	display, alarm, alarmTime, days       string
	live                                  bool
	offlineFor                            time.Duration
}

var demoSpecs = []demoSpec{
	{"Bedroom clock", "A1B2C3D4E5F6", "Bedroom", "Banja Luka", "44.772186", "17.191000", "seg7", "on", "06:30", "weekdays", true, 0},
	{"Kitchen clock", "3C71BF0A9E12", "Kitchen", "Banja Luka", "44.772186", "17.191000", "lcd", "off", "07:00", "all", true, 0},
	{"Office clock", "7CDFA1B3C4D5", "Office", "Sarajevo", "43.856430", "18.413029", "seg7", "off", "07:00", "all", false, 52 * time.Hour},
}

// seedDemo adds three clocks to the owner's account: two that check in live
// and one that went offline two days ago, with two weeks of history. Runs once.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	ctx = context.WithoutCancel(ctx)
	key := fmt.Sprintf("seeded:%d:demo", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	k, _ := kinds.Get("ntp-clock")
	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	for i, sp := range demoSpecs {
		d := &store.Device{Kind: k.ID, Name: sp.name, HWID: sp.hwid, Location: sp.location, TokenHash: store.HashToken(store.RandomToken(20)),
			TokenHint: "demo00", PollS: 30, NotifyOffline: true, Demo: sp.live}
		if err := s.store.CreateDevice(ctx, owner, d, "demo"); err != nil {
			return err
		}
		c := newDemoClock(sp.hwid, int64(i+1), firstNonEmpty(s.cfg.BaseURL, "https://devices.example.com"))
		c.Set("place", sp.place)
		c.Set("lat", sp.lat)
		c.Set("lon", sp.lon)
		c.Set("display", sp.display)
		// what the person set here over the last weeks
		desired := map[string]string{"tz": "CET-1CEST,M3.5.0,M10.5.0/3", "place": sp.place, "lat": sp.lat, "lon": sp.lon, "alarm": sp.alarm, "alarm_time": sp.alarmTime, "alarm_days": sp.days}
		if i == 0 {
			desired["brightness"], desired["bright_auto"], desired["chime"] = "40", "off", "off"
		}
		if _, _, err := s.store.SetDesired(ctx, owner, d.ID, k, desired, "Aleksandar", "setting"); err != nil {
			return err
		}
		// the first check-in takes the revision, the second confirms it
		for range 2 {
			req := c.Request()
			if out, err := s.syncDevice(ctx, d, mustMarshal(req), parseReq(req), c.IP); err == nil {
				c.Apply(toResp(out))
			}
		}
		// history: two weeks of temperature and signal
		drift := 0.0
		for t := now.Add(-14 * 24 * time.Hour); t.Before(now); t = t.Add(5 * time.Minute) {
			if !sp.live && t.After(now.Add(-sp.offlineFor)) {
				break
			}
			h := float64(t.Hour()) + float64(t.Minute())/60
			day := float64(t.YearDay())
			temp := 5 + 3*math.Sin(day/3) + 4.5*math.Sin((h-9)/24*2*math.Pi) + rng.Float64()*0.6
			temp = math.Round(temp*10) / 10
			drift += (rng.Float64() - .5) * .8
			drift = math.Max(-4, math.Min(4, drift))
			rssi := -52 - i*7 + int(math.Round(drift+1.5*math.Sin(h/24*2*math.Pi)))
			if err := s.store.AddSample(ctx, owner, d.ID, t, &rssi, &temp); err != nil {
				return err
			}
		}
		if !sp.live {
			seen := now.Add(-sp.offlineFor).UTC().Format(time.RFC3339Nano)
			_, _ = s.store.DB.ExecContext(ctx, `UPDATE devices SET last_seen_at = ?, online = 0, first_seen_at = ? WHERE id = ?`, seen, now.Add(-40*24*time.Hour).UTC().Format(time.RFC3339Nano), d.ID)
			_ = s.store.AddLog(ctx, owner, d.ID, store.LogEntry{Actor: "device", Action: "offline", Detail: "stopped checking in"})
			// changed while it was away: waits for its return
			_, _, _ = s.store.SetDesired(ctx, owner, d.ID, k, map[string]string{"brightness": "70", "show_seconds": "off"}, "Aleksandar", "setting")
			continue
		}
		if i == 1 {
			for _, name := range []string{"sync_time", "weather_refresh"} {
				_, _ = s.store.QueueCommand(ctx, owner, d.ID, name, nil, "Aleksandar")
			}
		}
		s.demoMu.Lock()
		s.demo[d.ID] = &demoClock{clock: c, nudge: make(chan struct{}, 1), ring: i == 0}
		s.demoMu.Unlock()
	}
	return s.store.CachePut(ctx, key, "1")
}

func newDemoClock(hwid string, seed int64, url string) *clocksim.Clock {
	c := clocksim.New(hwid, seed)
	c.Set("ssid", "Apolyx")
	c.Set("pass", "correct-horse")
	c.Set("tz", "CET-1CEST,M3.5.0,M10.5.0/3")
	c.Set("server_url", url)
	c.Set("device_token", "demo")
	return c
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func parseReq(r clocksim.Request) syncReq {
	var out syncReq
	_ = json.Unmarshal(mustMarshal(r), &out)
	return out
}

func toResp(m map[string]any) clocksim.Response {
	var r clocksim.Response
	_ = json.Unmarshal(mustMarshal(m), &r)
	return r
}

// demoLoop lets the simulated clocks check in every poll interval (or a few
// seconds after a change, as if the interval had come round).
func (s *Server) demoLoop(ctx context.Context) {
	// clocks seeded in an earlier run come back to life with their settings
	if ds, err := s.store.DemoDevices(ctx); err == nil {
		for i, d := range ds {
			s.demoMu.Lock()
			if _, ok := s.demo[d.ID]; !ok {
				c := newDemoClock(d.HWID, int64(i+1), firstNonEmpty(s.cfg.BaseURL, "https://devices.example.com"))
				for key, v := range d.Reported {
					if v != kinds.SecretMask {
						c.Set(key, v)
					}
				}
				c.SetRev(d.AppliedRev)
				s.demo[d.ID] = &demoClock{clock: c, nudge: make(chan struct{}, 1), ring: i == 0}
			}
			s.demoMu.Unlock()
		}
	}
	s.demoMu.Lock()
	clocks := map[int64]*demoClock{}
	for id, c := range s.demo {
		clocks[id] = c
	}
	s.demoMu.Unlock()
	for id, c := range clocks {
		go s.runDemoClock(ctx, id, c)
	}
}

func (s *Server) runDemoClock(ctx context.Context, id int64, dc *demoClock) {
	ringAt := time.Now().Add(2 * time.Minute)
	for {
		poll := time.Duration(max(dc.clock.PollS, 10)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		case <-dc.nudge:
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
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
		if dc.ring && time.Now().After(ringAt) {
			dc.clock.SetRinging(true)
			ringAt = time.Now().Add(6 * time.Hour)
		}
		req := dc.clock.Request()
		out, err := s.syncDevice(ctx, d, mustMarshal(req), parseReq(req), dc.clock.IP)
		if err != nil {
			s.log.Debug("demo clock", "err", err)
			continue
		}
		dc.clock.Apply(toResp(out))
	}
}

// demoNudge makes a simulated clock check in soon after a change.
func (s *Server) demoNudge(id int64) {
	s.demoMu.Lock()
	dc, ok := s.demo[id]
	s.demoMu.Unlock()
	if !ok {
		return
	}
	select {
	case dc.nudge <- struct{}{}:
	default:
	}
}
