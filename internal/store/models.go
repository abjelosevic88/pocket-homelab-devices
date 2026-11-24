package store

import "encoding/json"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Device is one piece of hardware a person owns.
type Device struct {
	ID            int64             `json:"id"`
	UserID        int64             `json:"-"`
	Kind          string            `json:"kind"`
	Name          string            `json:"name"`
	HWID          string            `json:"hw_id"`
	Location      string            `json:"location"`
	Notes         string            `json:"notes"`
	TokenHash     string            `json:"-"`
	TokenHint     string            `json:"token_hint"`
	TokenAt       string            `json:"token_at"`
	PollS         int               `json:"poll_s"`
	DesiredRev    int64             `json:"desired_rev"`
	AppliedRev    int64             `json:"applied_rev"`
	FirstSeenAt   string            `json:"first_seen_at"`
	LastSeenAt    string            `json:"last_seen_at"`
	LastIP        string            `json:"last_ip"`
	Status        json.RawMessage   `json:"status"`
	Reported      map[string]string `json:"-"`
	ConfigErrors  map[string]string `json:"config_errors"`
	Alerts        map[string]string `json:"-"` // alert id → since
	Online        bool              `json:"-"`
	NotifyOffline bool              `json:"notify_offline"`
	Demo          bool              `json:"demo"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

// DesiredSetting is a value set here for a device.
type DesiredSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Rev       int64  `json:"rev"`
	UpdatedAt string `json:"updated_at"`
}

// Command is something queued for a device.
type Command struct {
	ID        int64           `json:"id"`
	UserID    int64           `json:"-"`
	DeviceID  int64           `json:"device_id"`
	Name      string          `json:"name"`
	Args      json.RawMessage `json:"args"`
	State     string          `json:"state"`
	Result    string          `json:"result"`
	Actor     string          `json:"actor"`
	CreatedAt string          `json:"created_at"`
	SentAt    string          `json:"sent_at"`
	DoneAt    string          `json:"done_at"`
}

// LogEntry is one line of a device's history.
type LogEntry struct {
	ID       int64  `json:"id"`
	DeviceID int64  `json:"device_id"`
	At       string `json:"at"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Key      string `json:"key"`
	Old      string `json:"old"`
	New      string `json:"new"`
	Detail   string `json:"detail"`
}

// Event is something a device told as it happened (a press, a door opening).
type Event struct {
	ID       int64          `json:"id"`
	DeviceID int64          `json:"device_id"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
	At       string         `json:"at"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections. Connection modes: "core" (an app link through Pocket
// Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`
	// DeviceURL is the address devices are told to use (cfg set server_url);
	// empty = BASE_URL. Box-wide: one server, one address.
	DeviceURL string `json:"device_url"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// Notifications.
	NotifyOffline bool   `json:"notify_offline"` // a device stopped checking in
	NotifyRefused bool   `json:"notify_refused"` // a device refused a setting
	NotifyAlerts  bool   `json:"notify_alerts"`  // a reading crossed a line: water, battery low, CO2 high
	NotifyURL     string `json:"notify_url"`
	NotifyFormat  string `json:"notify_format"` // apprise | ntfy | json

	// Pocket Weather: pick a clock's place from your places.
	WeatherMode      string `json:"weather_mode"`
	WeatherURL       string `json:"weather_url"`
	WeatherToken     string `json:"weather_token"`
	WeatherPublicURL string `json:"weather_public_url"`
}
