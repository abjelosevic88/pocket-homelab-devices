// Package kinds holds the hardware kinds Pocket Devices knows: one JSON file
// per kind with its setting table (key, type, range, default, group, label,
// help), its commands and how it is set up. The validator and the web UI both
// read the same file, so a new setting or a new kind of device needs no code.
package kinds

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.json
var files embed.FS

// Option is one allowed value of an enum (or a labelled number). Setting,
// on an event field's option, names the device setting that says what it
// means to the person (a button's "double_name": "Focus timer").
type Option struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Setting string `json:"setting,omitempty"`
}

// Setting is one row of a kind's setting table. Values travel as strings in
// the device's own console form (on/off, HH:MM, mon-fri, decimal degrees).
type Setting struct {
	Key        string   `json:"key"`
	Type       string   `json:"type"` // text | number | bool | hhmm | days | enum | coord
	Group      string   `json:"group"`
	Label      string   `json:"label"`
	Help       string   `json:"help"`
	Default    string   `json:"default"`
	Min        *float64 `json:"min,omitempty"` // length for text, value for number/coord
	Max        *float64 `json:"max,omitempty"`
	AllowEmpty bool     `json:"allow_empty,omitempty"` // text: "" is fine although Min > 0
	Format     string   `json:"format,omitempty"`      // text: tz | https_url | hostname | host | printable
	Options    []Option `json:"options,omitempty"`
	Presets    []Option `json:"presets,omitempty"` // suggestions for a text field (time zones)
	Unit       string   `json:"unit,omitempty"`
	Decimals   int      `json:"decimals,omitempty"` // decimal: places after the point
	Secret     bool     `json:"secret,omitempty"`   // the device reports "****"; never shown in the browser
	Locked     bool     `json:"locked,omitempty"`   // never changed by the server
	Reboot     bool     `json:"reboot,omitempty"`   // takes effect after a restart
	Warn       string   `json:"warn,omitempty"`     // a key of Kind.Warnings shown when it changes
	Advanced   bool     `json:"advanced,omitempty"` // folded away in the UI
	DefaultOK  bool     `json:"default_ok"`         // the default passes the validator ("reset to default" is offered)
}

// Arg is an argument of a command.
type Arg struct {
	Name    string  `json:"name"`
	Label   string  `json:"label"`
	Type    string  `json:"type"` // number
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
	// DefaultSetting takes the default from the device's setting (the clock's alarm sound).
	DefaultSetting string `json:"default_setting,omitempty"`
}

// Reading is something a device reports: a temperature, a battery level,
// whether a door is open. Path is where it sits in the report ("climate.temp_c"),
// or "config.<key>" for one of the device's own settings.
type Reading struct {
	Key      string   `json:"key"`
	Path     string   `json:"path"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // number | bool | text
	Unit     string   `json:"unit,omitempty"`
	Decimals int      `json:"decimals,omitempty"`
	Icon     string   `json:"icon,omitempty"`
	On       string   `json:"on,omitempty"`  // bool: the word for true ("Open")
	Off      string   `json:"off,omitempty"` // bool: the word for false ("Closed")
	Chart    bool     `json:"chart,omitempty"`
	Card     bool     `json:"card,omitempty"`    // shown on the device's card in the list
	Sub      string   `json:"sub,omitempty"`     // another reading shown under this one
	Min      float64  `json:"min,omitempty"`     // chart floor (0 = automatic)
	Options  []Option `json:"options,omitempty"` // text: labels for the values the device sends
}

// Alert is a condition on a reading the person hears about: battery low,
// water detected, CO2 too high. The threshold is a fixed value or one of the
// device's settings; EnabledSetting switches it off with a device setting.
type Alert struct {
	ID             string  `json:"id"`
	Reading        string  `json:"reading"`
	Op             string  `json:"op"` // < | > | =
	Value          float64 `json:"value,omitempty"`
	Bool           *bool   `json:"bool,omitempty"`    // for = on a bool reading
	Setting        string  `json:"setting,omitempty"` // the threshold is this setting's value
	EnabledSetting string  `json:"enabled_setting,omitempty"`
	Label          string  `json:"label"`
	Text           string  `json:"text,omitempty"`   // the message; {value} {unit} {name} are filled in
	Level          string  `json:"level"`            // info | warning | alarm
	Action         string  `json:"action,omitempty"` // a command offered next to it ("alarm_stop")
}

// EventField is one field of a device event, with its allowed values.
type EventField struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Options []Option `json:"options,omitempty"`
}

// Event is something a device tells as it happens: a button press, a door
// opening, motion. Sent in the optional "events" field of the sync request.
type Event struct {
	Type   string       `json:"type"`
	Label  string       `json:"label"`
	Icon   string       `json:"icon,omitempty"`
	Fields []EventField `json:"fields,omitempty"`
	Notify bool         `json:"notify,omitempty"` // tell the person every time (water!)
}

// Command is something the device can be told to do now.
type Command struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Help   string `json:"help"`
	Danger int    `json:"danger,omitempty"` // 0 everyday, 1 confirm once, 2 confirm twice
	Args   []Arg  `json:"args,omitempty"`
}

// Group is a heading of the settings page, in the device's own order.
type Group struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Kind is one kind of hardware.
type Kind struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Summary  string `json:"summary"`
	Icon     string `json:"icon"`
	Category string `json:"category"` // clock | sensor | button | switch
	Power    string `json:"power"`    // mains | usb | battery
	HWID     struct {
		Label   string `json:"label"`
		Help    string `json:"help"`
		Example string `json:"example"`
		Format  string `json:"format"` // mac12
	} `json:"hw_id"`
	Token struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"token"`
	Poll struct {
		Default int `json:"default"`
		Min     int `json:"min"`
		Max     int `json:"max"`
	} `json:"poll"`
	Setup     []string          `json:"setup"`
	SetupHelp string            `json:"setup_help"`
	Groups    []Group           `json:"groups"`
	Settings  []Setting         `json:"settings"`
	Commands  []Command         `json:"commands"`
	Readings  []Reading         `json:"readings"`
	Alerts    []Alert           `json:"alerts"`
	Events    []Event           `json:"events"`
	Warnings  map[string]string `json:"warnings"`

	byKey map[string]*Setting
}

var (
	all  []*Kind
	byID = map[string]*Kind{}
)

func init() {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		b, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		k, err := Parse(b)
		if err != nil {
			panic(fmt.Sprintf("kinds/%s: %v", e.Name(), err))
		}
		all = append(all, k)
		byID[k.ID] = k
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
}

// Parse reads and checks one kind file.
func Parse(b []byte) (*Kind, error) {
	var k Kind
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, err
	}
	if k.ID == "" || k.Name == "" {
		return nil, fmt.Errorf("id and name are required")
	}
	groups := map[string]bool{}
	for _, g := range k.Groups {
		groups[g.ID] = true
	}
	k.byKey = map[string]*Setting{}
	for i := range k.Settings {
		s := &k.Settings[i]
		if s.Key == "" || k.byKey[s.Key] != nil {
			return nil, fmt.Errorf("setting %d: empty or duplicate key %q", i, s.Key)
		}
		if !groups[s.Group] {
			return nil, fmt.Errorf("setting %s: unknown group %q", s.Key, s.Group)
		}
		switch s.Type {
		case "text", "number", "bool", "hhmm", "days", "enum", "coord", "decimal":
		default:
			return nil, fmt.Errorf("setting %s: unknown type %q", s.Key, s.Type)
		}
		k.byKey[s.Key] = s
		_, err := k.Normalize(s.Key, s.Default)
		s.DefaultOK = err == nil
	}
	if k.Token.Min == 0 {
		k.Token.Min, k.Token.Max = 32, 64
	}
	readings := map[string]*Reading{}
	for i := range k.Readings {
		r := &k.Readings[i]
		if r.Key == "" || readings[r.Key] != nil || r.Path == "" {
			return nil, fmt.Errorf("reading %d: empty or duplicate key %q, or no path", i, r.Key)
		}
		switch r.Type {
		case "number", "bool", "text":
		default:
			return nil, fmt.Errorf("reading %s: unknown type %q", r.Key, r.Type)
		}
		if cfg, ok := strings.CutPrefix(r.Path, "config."); ok && k.byKey[cfg] == nil {
			return nil, fmt.Errorf("reading %s: no setting %q", r.Key, cfg)
		}
		readings[r.Key] = r
	}
	for _, r := range k.Readings {
		if r.Sub != "" && readings[r.Sub] == nil {
			return nil, fmt.Errorf("reading %s: sub %q is not a reading", r.Key, r.Sub)
		}
	}
	for i, a := range k.Alerts {
		r := readings[a.Reading]
		if a.ID == "" || r == nil {
			return nil, fmt.Errorf("alert %d (%s): unknown reading %q", i, a.ID, a.Reading)
		}
		switch a.Op {
		case "<", ">", "=":
		default:
			return nil, fmt.Errorf("alert %s: op is <, > or =", a.ID)
		}
		if (r.Type == "bool") != (a.Bool != nil) {
			return nil, fmt.Errorf("alert %s: a bool reading needs \"bool\", a number reading a value", a.ID)
		}
		for _, key := range []string{a.Setting, a.EnabledSetting} {
			if key != "" && k.byKey[key] == nil {
				return nil, fmt.Errorf("alert %s: no setting %q", a.ID, key)
			}
		}
		if a.Action != "" {
			if _, ok := k.Command(a.Action); !ok {
				return nil, fmt.Errorf("alert %s: no command %q", a.ID, a.Action)
			}
		}
	}
	seenEv := map[string]bool{}
	for _, e := range k.Events {
		if e.Type == "" || seenEv[e.Type] {
			return nil, fmt.Errorf("event: empty or duplicate type %q", e.Type)
		}
		seenEv[e.Type] = true
	}
	for _, c := range k.Commands {
		for _, a := range c.Args {
			if a.DefaultSetting != "" && k.byKey[a.DefaultSetting] == nil {
				return nil, fmt.Errorf("command %s: no setting %q", c.Name, a.DefaultSetting)
			}
		}
	}
	if k.Poll.Default == 0 {
		k.Poll.Default, k.Poll.Min, k.Poll.Max = 30, 10, 3600
	}
	return &k, nil
}

// All lists every kind.
func All() []*Kind { return all }

// Get returns a kind by id.
func Get(id string) (*Kind, bool) {
	k, ok := byID[id]
	return k, ok
}

// Setting returns one row of the table.
func (k *Kind) Setting(key string) (*Setting, bool) {
	s, ok := k.byKey[key]
	return s, ok
}

// Command returns a command by name.
func (k *Kind) Command(name string) (*Command, bool) {
	for i := range k.Commands {
		if k.Commands[i].Name == name {
			return &k.Commands[i], true
		}
	}
	return nil, false
}

// Warning is the text of a setting's warning ("" when it has none).
func (k *Kind) Warning(key string) string {
	if s, ok := k.byKey[key]; ok && s.Warn != "" {
		return k.Warnings[s.Warn]
	}
	return ""
}

var (
	reMAC      = regexp.MustCompile(`^[0-9A-F]{12}$`)
	reSerial   = regexp.MustCompile(`^[0-9A-Z][0-9A-Z-]{2,30}[0-9A-Z]$`)
	reHHMM     = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)
	reInt      = regexp.MustCompile(`^[+-]?\d+$`)
	reDecimal  = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)
	reCoord    = regexp.MustCompile(`^[+-]?\d{1,3}(\.\d{1,6})?$`)
	reHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
	reHost     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	// A POSIX TZ string: std offset [dst [offset] [,start[/time],end[/time]]].
	tzName = `(?:[A-Za-z]{3,}|<[A-Za-z0-9+-]{3,}>)`
	tzOff  = `[+-]?\d{1,2}(?::\d{2}){0,2}`
	tzRule = `(?:J\d{1,3}|\d{1,3}|M\d{1,2}\.\d\.\d)(?:/[+-]?\d{1,3}(?::\d{2}){0,2})?`
	reTZ   = regexp.MustCompile(`^` + tzName + tzOff + `(?:` + tzName + `(?:` + tzOff + `)?(?:,` + tzRule + `,` + tzRule + `)?)?$`)
)

// NormalizeHWID checks how a device names itself: "mac12" turns
// "a1:b2:c3:d4:e5:f6" into "A1B2C3D4E5F6"; "serial" is 4 to 32 letters,
// digits and dashes, upper case.
func (k *Kind) NormalizeHWID(v string) (string, error) {
	if k.HWID.Format == "serial" {
		v = strings.ToUpper(strings.TrimSpace(v))
		if !reSerial.MatchString(v) {
			return "", fmt.Errorf("a serial number is 4 to 32 letters, digits and dashes, like %s", k.HWID.Example)
		}
		return v, nil
	}
	v = strings.ToUpper(strings.NewReplacer(":", "", "-", "", " ", "", ".", "").Replace(strings.TrimSpace(v)))
	if !reMAC.MatchString(v) {
		return "", fmt.Errorf("a MAC address is 12 characters 0-9 and A-F, like %s", k.HWID.Example)
	}
	return v, nil
}

// Reading returns a reading by key.
func (k *Kind) Reading(key string) (*Reading, bool) {
	for i := range k.Readings {
		if k.Readings[i].Key == key {
			return &k.Readings[i], true
		}
	}
	return nil, false
}

// Event returns an event type.
func (k *Kind) Event(typ string) (*Event, bool) {
	for i := range k.Events {
		if k.Events[i].Type == typ {
			return &k.Events[i], true
		}
	}
	return nil, false
}

// Values reads every reading out of a report (status) and the reported
// settings: numbers as float64, bools as bool, text as string. A reading the
// device did not send is left out.
func (k *Kind) Values(status map[string]any, reported map[string]string) map[string]any {
	out := map[string]any{}
	for _, r := range k.Readings {
		var raw any
		if cfg, ok := strings.CutPrefix(r.Path, "config."); ok {
			v, ok := reported[cfg]
			if !ok {
				continue
			}
			raw = v
		} else {
			v, ok := lookup(status, r.Path)
			if !ok || v == nil {
				continue
			}
			raw = v
		}
		switch r.Type {
		case "number":
			switch x := raw.(type) {
			case float64:
				out[r.Key] = x
			case string:
				if f, err := strconv.ParseFloat(x, 64); err == nil {
					out[r.Key] = f
				}
			}
		case "bool":
			switch x := raw.(type) {
			case bool:
				out[r.Key] = x
			case float64:
				out[r.Key] = x != 0
			case string:
				out[r.Key] = x == "on" || x == "true" || x == "1"
			}
		default:
			out[r.Key] = fmt.Sprint(raw)
		}
	}
	return out
}

func lookup(m map[string]any, path string) (any, bool) {
	head, rest, nested := strings.Cut(path, ".")
	v, ok := m[head]
	if !ok {
		return nil, false
	}
	if !nested {
		return v, true
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookup(sub, rest)
}

// ActiveAlerts are the alerts whose condition holds now, by id. Settings
// (thresholds, on/off switches) come from what the device reports.
func (k *Kind) ActiveAlerts(values map[string]any, settings map[string]string) []string {
	var out []string
	for _, a := range k.Alerts {
		if a.EnabledSetting != "" && settings[a.EnabledSetting] != "on" {
			continue
		}
		v, ok := values[a.Reading]
		if !ok {
			continue
		}
		if a.Bool != nil {
			if b, ok := v.(bool); ok && b == *a.Bool {
				out = append(out, a.ID)
			}
			continue
		}
		f, ok := v.(float64)
		if !ok {
			continue
		}
		limit := a.Value
		if a.Setting != "" {
			s, err := strconv.ParseFloat(settings[a.Setting], 64)
			if err != nil {
				continue
			}
			limit = s
		}
		if (a.Op == "<" && f < limit) || (a.Op == ">" && f > limit) || (a.Op == "=" && f == limit) {
			out = append(out, a.ID)
		}
	}
	return out
}

// Alert returns an alert by id.
func (k *Kind) Alert(id string) (*Alert, bool) {
	for i := range k.Alerts {
		if k.Alerts[i].ID == id {
			return &k.Alerts[i], true
		}
	}
	return nil, false
}

// Days are the weekday names of the days type, Monday first.
var Days = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func dayIndex(d string) int {
	for i, x := range Days {
		if x == d {
			return i
		}
	}
	return -1
}

// Normalize checks a value against the table (same rules as the device's
// console) and returns it in the device's canonical form.
func (k *Kind) Normalize(key, v string) (string, error) {
	s, ok := k.byKey[key]
	if !ok {
		return "", fmt.Errorf("unknown setting")
	}
	return s.Normalize(v)
}

// Normalize checks one value of this setting.
func (s *Setting) Normalize(v string) (string, error) {
	switch s.Type {
	case "bool":
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "on", "true", "1", "yes":
			return "on", nil
		case "off", "false", "0", "no":
			return "off", nil
		}
		return "", fmt.Errorf("on or off")
	case "number":
		v = strings.TrimSpace(v)
		if !reInt.MatchString(v) {
			return "", fmt.Errorf("a whole number")
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "", fmt.Errorf("a whole number")
		}
		if (s.Min != nil && float64(n) < *s.Min) || (s.Max != nil && float64(n) > *s.Max) {
			return "", fmt.Errorf("%s to %s", num(s.Min), num(s.Max))
		}
		if len(s.Options) > 0 && !hasOption(s.Options, strconv.FormatInt(n, 10)) {
			return "", fmt.Errorf("not one of the choices")
		}
		return strconv.FormatInt(n, 10), nil
	case "coord":
		v = strings.TrimSpace(v)
		if !reCoord.MatchString(v) {
			return "", fmt.Errorf("decimal degrees with up to 6 decimals, like 44.772186")
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) {
			return "", fmt.Errorf("decimal degrees")
		}
		if (s.Min != nil && f < *s.Min) || (s.Max != nil && f > *s.Max) {
			return "", fmt.Errorf("%s to %s", num(s.Min), num(s.Max))
		}
		out := strconv.FormatFloat(f, 'f', 6, 64)
		if out == "-0.000000" {
			out = "0.000000"
		}
		return out, nil
	case "decimal":
		v = strings.TrimSpace(strings.Replace(v, ",", ".", 1))
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || !reDecimal.MatchString(v) {
			return "", fmt.Errorf("a number like 1.5")
		}
		if (s.Min != nil && f < *s.Min) || (s.Max != nil && f > *s.Max) {
			return "", fmt.Errorf("%s to %s", num(s.Min), num(s.Max))
		}
		if i := strings.IndexByte(v, '.'); i >= 0 && len(v)-i-1 > s.Decimals {
			return "", fmt.Errorf("at most %d decimals", s.Decimals)
		}
		out := strconv.FormatFloat(f, 'f', s.Decimals, 64)
		if strings.Trim(out, "-0.") == "" {
			out = strings.TrimPrefix(out, "-")
		}
		return out, nil
	case "hhmm":
		m := reHHMM.FindStringSubmatch(strings.TrimSpace(v))
		if m == nil {
			return "", fmt.Errorf("a time 00:00 to 23:59")
		}
		h, _ := strconv.Atoi(m[1])
		return fmt.Sprintf("%02d:%s", h, m[2]), nil
	case "days":
		return normalizeDays(v)
	case "enum":
		v = strings.ToLower(strings.TrimSpace(v))
		if !hasOption(s.Options, v) {
			vals := make([]string, len(s.Options))
			for i, o := range s.Options {
				vals[i] = o.Value
			}
			return "", fmt.Errorf("one of %s", strings.Join(vals, ", "))
		}
		return v, nil
	case "text":
		if strings.ContainsAny(v, "\r\n\t\x00") {
			return "", fmt.Errorf("one line of text")
		}
		n := len([]rune(v))
		if n == 0 && s.AllowEmpty {
			return v, nil
		}
		if s.Min != nil && float64(n) < *s.Min {
			if s.AllowEmpty {
				return "", fmt.Errorf("empty, or %s to %s characters", num(s.Min), num(s.Max))
			}
			if *s.Min == 1 {
				return "", fmt.Errorf("cannot be empty")
			}
			return "", fmt.Errorf("at least %s characters", num(s.Min))
		}
		if s.Max != nil && float64(n) > *s.Max {
			return "", fmt.Errorf("at most %s characters", num(s.Max))
		}
		if len(v) != n {
			// multi-byte text: the device counts bytes
			if s.Max != nil && float64(len(v)) > *s.Max {
				return "", fmt.Errorf("at most %s characters", num(s.Max))
			}
		}
		switch s.Format {
		case "tz":
			if !reTZ.MatchString(v) {
				return "", fmt.Errorf("a POSIX time zone like CET-1CEST,M3.5.0,M10.5.0/3")
			}
		case "https_url":
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || u.Host == "" || strings.HasSuffix(v, "/") || u.RawQuery != "" || u.Fragment != "" {
				return "", fmt.Errorf("an https:// address without a trailing slash")
			}
		case "hostname":
			if !reHostname.MatchString(v) {
				return "", fmt.Errorf("letters, digits and hyphens only")
			}
		case "host":
			if !reHost.MatchString(v) {
				return "", fmt.Errorf("a host name like pool.ntp.org")
			}
		case "printable":
			for _, r := range v {
				if r < 0x20 || r > 0x7e {
					return "", fmt.Errorf("plain letters, digits and punctuation only")
				}
			}
		}
		return v, nil
	}
	return "", fmt.Errorf("unknown type")
}

// normalizeDays accepts all, weekdays, weekend, none, a list "mon,wed,fri"
// or one range "mon-fri".
func normalizeDays(v string) (string, error) {
	v = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
	bad := fmt.Errorf("all, weekdays, weekend, none, a list like mon,wed,fri or a range like mon-fri")
	switch v {
	case "all", "weekdays", "weekend", "none":
		return v, nil
	case "":
		return "", bad
	}
	if a, b, ok := strings.Cut(v, "-"); ok {
		if strings.Contains(v, ",") || dayIndex(a) < 0 || dayIndex(b) < 0 {
			return "", bad
		}
		return v, nil
	}
	seen := map[string]bool{}
	for _, d := range strings.Split(v, ",") {
		if dayIndex(d) < 0 || seen[d] {
			return "", bad
		}
		seen[d] = true
	}
	return v, nil
}

func hasOption(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func num(f *float64) string {
	if f == nil {
		return "?"
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

// Equal compares two values of a setting by meaning (on = true, 7:00 = 07:00).
func (k *Kind) Equal(key, a, b string) bool {
	if a == b {
		return true
	}
	na, err1 := k.Normalize(key, a)
	nb, err2 := k.Normalize(key, b)
	return err1 == nil && err2 == nil && na == nb
}

// SecretMask is what a device sends for a secret it has.
const SecretMask = "****"
