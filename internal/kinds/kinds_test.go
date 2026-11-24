package kinds

import (
	"strconv"
	"strings"
	"testing"
)

func clock(t *testing.T) *Kind {
	t.Helper()
	k, ok := Get("ntp-clock")
	if !ok {
		t.Fatal("ntp-clock kind missing")
	}
	return k
}

// The table from server-protocol.md: every key, its type and its default.
var protocolTable = map[string][2]string{
	"ssid": {"text", ""}, "pass": {"text", ""}, "hostname": {"text", "apx-ntp-clock"}, "ntp_server": {"text", "pool.ntp.org"},
	"ntp_interval": {"number", "60"}, "sync_timeout": {"number", "180"}, "tz": {"text", "UTC0"}, "fmt24h": {"bool", "on"},
	"show_seconds": {"bool", "on"}, "leading_zero": {"bool", "on"}, "date_fmt": {"number", "0"}, "display": {"enum", "seg7"},
	"splash_text": {"text", "HELLO"}, "splash_secs": {"number", "5"}, "scroll_ms": {"number", "300"}, "clock_secs": {"number", "30"},
	"date_secs": {"number", "5"}, "temp_secs": {"number", "3"}, "alarm_page_secs": {"number", "3"}, "temp_unit": {"enum", "c"},
	"brightness": {"number", "100"}, "bright_auto": {"bool", "on"}, "bright_min": {"number", "10"}, "ldr_dark_mv": {"number", "60"},
	"ldr_bright_mv": {"number", "1800"}, "alarm": {"bool", "off"}, "alarm_time": {"hhmm", "07:00"}, "alarm_days": {"days", "all"},
	"alarm_secs": {"number", "60"}, "alarm_sound": {"number", "0"}, "chime": {"bool", "on"}, "status_beep": {"bool", "on"},
	"quiet_from": {"hhmm", "22:00"}, "quiet_to": {"hhmm", "07:00"}, "place": {"text", ""}, "lat": {"coord", "0"}, "lon": {"coord", "0"},
	"weather_poll": {"number", "30"}, "wifi_tx": {"number", "20"}, "ap_fallback_n": {"number", "3"}, "log": {"bool", "off"},
	"remote": {"bool", "off"}, "server_url": {"text", ""}, "device_token": {"text", ""}, "remote_poll_s": {"number", "30"},
}

// Ranges from the protocol table: number = value range, text = length.
var protocolRanges = map[string][2]int{
	"ssid": {1, 32}, "pass": {8, 64}, "hostname": {1, 32}, "ntp_server": {1, 64}, "ntp_interval": {15, 1440}, "sync_timeout": {15, 10080},
	"tz": {1, 64}, "date_fmt": {0, 2}, "splash_text": {0, 64}, "splash_secs": {1, 600}, "scroll_ms": {50, 2000}, "clock_secs": {1, 3600},
	"date_secs": {0, 3600}, "temp_secs": {0, 3600}, "alarm_page_secs": {0, 3600}, "brightness": {1, 100}, "bright_min": {1, 100},
	"ldr_dark_mv": {1, 3300}, "ldr_bright_mv": {2, 3300}, "alarm_secs": {1, 600}, "alarm_sound": {0, 6}, "place": {0, 47},
	"lat": {-90, 90}, "lon": {-180, 180}, "weather_poll": {5, 1440}, "wifi_tx": {2, 20}, "ap_fallback_n": {0, 100},
	"server_url": {1, 96}, "device_token": {32, 64}, "remote_poll_s": {10, 3600},
}

func TestTableMatchesProtocol(t *testing.T) {
	k := clock(t)
	if len(k.Settings) != len(protocolTable) {
		t.Fatalf("%d settings, the protocol has %d", len(k.Settings), len(protocolTable))
	}
	for key, want := range protocolTable {
		s, ok := k.Setting(key)
		if !ok {
			t.Errorf("%s missing", key)
			continue
		}
		if s.Type != want[0] || s.Default != want[1] {
			t.Errorf("%s: type %s default %q, want %s %q", key, s.Type, s.Default, want[0], want[1])
		}
		if r, ok := protocolRanges[key]; ok {
			if s.Min == nil || s.Max == nil || int(*s.Min) != r[0] || int(*s.Max) != r[1] {
				t.Errorf("%s: range %v..%v, want %d..%d", key, s.Min, s.Max, r[0], r[1])
			}
		}
	}
	for _, key := range []string{"remote", "server_url", "device_token"} {
		if s, _ := k.Setting(key); !s.Locked {
			t.Errorf("%s must be locked (never changed by the server)", key)
		}
	}
	for _, key := range []string{"pass", "device_token"} {
		if s, _ := k.Setting(key); !s.Secret {
			t.Errorf("%s must be secret", key)
		}
	}
	if s, _ := k.Setting("display"); !s.Reboot {
		t.Error("display needs a reboot")
	}
	for _, g := range []string{"WiFi", "Time", "Display", "Pages and splash", "Weather", "Alarm", "Chime and beeps", "System", "Server"} {
		found := false
		for _, x := range k.Groups {
			found = found || x.Label == g
		}
		if !found {
			t.Errorf("group %s missing", g)
		}
	}
}

// Every row: the default (when the device's own default is valid), both
// ends of the range, one step outside, and a value of the wrong type.
func TestEveryRow(t *testing.T) {
	k := clock(t)
	for _, s := range k.Settings {
		t.Run(s.Key, func(t *testing.T) {
			ok := func(v string) {
				t.Helper()
				if _, err := s.Normalize(v); err != nil {
					t.Errorf("%q refused: %v", v, err)
				}
			}
			no := func(v string) {
				t.Helper()
				if n, err := s.Normalize(v); err == nil {
					t.Errorf("%q accepted as %q", v, n)
				}
			}
			if s.DefaultOK {
				ok(s.Default)
			}
			switch s.Type {
			case "bool":
				ok("on")
				ok("off")
				no("maybe")
				no("")
			case "number":
				lo, hi := int(*s.Min), int(*s.Max)
				ok(strconv.Itoa(lo))
				ok(strconv.Itoa(hi))
				no(strconv.Itoa(lo - 1))
				no(strconv.Itoa(hi + 1))
				no("1.5")
				no("ten")
				no("")
			case "coord":
				ok(strconv.Itoa(int(*s.Min)))
				ok(strconv.Itoa(int(*s.Max)))
				ok("12.345678")
				no(strconv.Itoa(int(*s.Max) + 1))
				no("1.1234567")
				no("north")
			case "hhmm":
				ok("00:00")
				ok("23:59")
				no("24:00")
				no("12:60")
				no("noon")
			case "days":
				for _, v := range []string{"all", "weekdays", "weekend", "none", "mon,wed,fri", "mon-fri", "sat"} {
					ok(v)
				}
				for _, v := range []string{"", "monday", "mon,mon", "mon-fri,sun", "xyz-fri"} {
					no(v)
				}
			case "enum":
				for _, o := range s.Options {
					ok(o.Value)
				}
				no("other")
			case "text":
				lo, hi := int(*s.Min), int(*s.Max)
				sample := func(n int) string {
					switch s.Format {
					case "tz":
						base := "CET-1CEST,M3.5.0,M10.5.0/3"
						if n < len("UTC0") {
							return strings.Repeat("U", n)
						}
						if n <= len(base) {
							return "UTC" + strings.Repeat("0", n-3)[:n-3]
						}
						return base + strings.Repeat("0", n-len(base))
					case "https_url":
						if n < len("https://a.b") {
							return strings.Repeat("h", n)
						}
						return "https://" + strings.Repeat("a", n-len("https://.b")) + ".b"
					}
					return strings.Repeat("a", n)
				}
				if s.Format != "tz" {
					ok(sample(hi))
					no(sample(hi + 1))
				}
				if lo > 0 && !s.AllowEmpty {
					no("")
				}
				if lo > 1 {
					no(sample(lo - 1))
				}
				if s.AllowEmpty {
					ok("")
				}
				no("two\nlines")
			}
		})
	}
}

func TestSpecialRules(t *testing.T) {
	k := clock(t)
	cases := []struct {
		key, in, out string
		ok           bool
	}{
		{"pass", "", "", true},
		{"pass", "short", "", false},
		{"pass", "longenough", "longenough", true},
		{"tz", "CET-1CEST,M3.5.0,M10.5.0/3", "CET-1CEST,M3.5.0,M10.5.0/3", true},
		{"tz", "EST5EDT,M3.2.0,M11.1.0", "EST5EDT,M3.2.0,M11.1.0", true},
		{"tz", "IST-5:30", "IST-5:30", true},
		{"tz", "<+03>-3", "<+03>-3", true},
		{"tz", "Europe/Sarajevo", "", false},
		{"tz", "CET", "", false},
		{"server_url", "https://devices.example.com", "https://devices.example.com", true},
		{"server_url", "http://devices.example.com", "", false},
		{"server_url", "https://devices.example.com/", "", false},
		{"alarm_time", "6:30", "06:30", true},
		{"fmt24h", "true", "on", true},
		{"lat", "44.7722", "44.772200", true},
		{"lat", "-0", "0.000000", true},
		{"display", "LCD", "lcd", true},
		{"alarm_days", "Mon, Wed", "mon,wed", true},
		{"hostname", "my clock", "", false},
		{"splash_text", "HÉLLO", "", false},
		{"date_fmt", "3", "", false},
	}
	for _, c := range cases {
		got, err := k.Normalize(c.key, c.in)
		if (err == nil) != c.ok || (c.ok && got != c.out) {
			t.Errorf("%s %q: got %q, %v; want %q ok=%v", c.key, c.in, got, err, c.out, c.ok)
		}
	}
	if _, err := k.Normalize("nope", "1"); err == nil {
		t.Error("unknown key accepted")
	}
	for _, p := range func() []Option { s, _ := k.Setting("tz"); return s.Presets }() {
		if _, err := k.Normalize("tz", p.Value); err != nil {
			t.Errorf("time zone preset %s (%s) refused: %v", p.Label, p.Value, err)
		}
	}
}

func TestEqualAndHWID(t *testing.T) {
	k := clock(t)
	if !k.Equal("lat", "44.7722", "44.772200") || !k.Equal("alarm_time", "7:00", "07:00") || k.Equal("brightness", "60", "61") {
		t.Error("Equal")
	}
	id, err := k.NormalizeHWID("a1:b2:c3:d4:e5:f6")
	if err != nil || id != "A1B2C3D4E5F6" {
		t.Errorf("hw id %q %v", id, err)
	}
	if _, err := k.NormalizeHWID("A1B2C3"); err == nil {
		t.Error("short MAC accepted")
	}
}
