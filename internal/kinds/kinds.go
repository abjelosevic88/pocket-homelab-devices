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

// Option is one allowed value of an enum (or a labelled number).
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
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
	Status    map[string]string `json:"status"`
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
		case "text", "number", "bool", "hhmm", "days", "enum", "coord":
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
	reHHMM     = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)
	reInt      = regexp.MustCompile(`^[+-]?\d+$`)
	reCoord    = regexp.MustCompile(`^[+-]?\d{1,3}(\.\d{1,6})?$`)
	reHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)
	reHost     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	// A POSIX TZ string: std offset [dst [offset] [,start[/time],end[/time]]].
	tzName = `(?:[A-Za-z]{3,}|<[A-Za-z0-9+-]{3,}>)`
	tzOff  = `[+-]?\d{1,2}(?::\d{2}){0,2}`
	tzRule = `(?:J\d{1,3}|\d{1,3}|M\d{1,2}\.\d\.\d)(?:/[+-]?\d{1,3}(?::\d{2}){0,2})?`
	reTZ   = regexp.MustCompile(`^` + tzName + tzOff + `(?:` + tzName + `(?:` + tzOff + `)?(?:,` + tzRule + `,` + tzRule + `)?)?$`)
)

// NormalizeHWID turns "a1:b2:c3:d4:e5:f6" into "A1B2C3D4E5F6" and checks it.
func (k *Kind) NormalizeHWID(v string) (string, error) {
	v = strings.ToUpper(strings.NewReplacer(":", "", "-", "", " ", "", ".", "").Replace(strings.TrimSpace(v)))
	if !reMAC.MatchString(v) {
		return "", fmt.Errorf("a MAC address is 12 characters 0-9 and A-F, like %s", k.HWID.Example)
	}
	return v, nil
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
