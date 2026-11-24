CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- One piece of hardware a person owns. kind = a file in internal/kinds;
-- hw_id = how the device names itself (the WiFi MAC for the NTP clock),
-- unique on the box because the device sends it before anyone is known.
CREATE TABLE devices (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,
  name          TEXT NOT NULL,
  hw_id         TEXT NOT NULL UNIQUE,
  location      TEXT NOT NULL DEFAULT '',
  notes         TEXT NOT NULL DEFAULT '',
  token_hash    TEXT NOT NULL,                   -- sha256 hex of the device token; the token itself is shown once
  token_hint    TEXT NOT NULL DEFAULT '',        -- its first characters, to recognise it
  token_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  poll_s        INTEGER NOT NULL DEFAULT 30,     -- check-in interval handed to the device
  desired_rev   INTEGER NOT NULL DEFAULT 0,      -- grows with every change made here
  applied_rev   INTEGER NOT NULL DEFAULT 0,      -- the revision the device says it applied
  first_seen_at TEXT NOT NULL DEFAULT '',
  last_seen_at  TEXT NOT NULL DEFAULT '',
  last_ip       TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT '{}',      -- the last report without its config
  reported      TEXT NOT NULL DEFAULT '{}',      -- the settings the device really has
  config_errors TEXT NOT NULL DEFAULT '{}',      -- settings it refused last time
  online        INTEGER NOT NULL DEFAULT 0,      -- last known state, for the went-offline / back-online events
  notify_offline INTEGER NOT NULL DEFAULT 1,
  demo          INTEGER NOT NULL DEFAULT 0,      -- a simulated device (DEMO_DATA)
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX devices_user ON devices(user_id, name);

-- The desired settings: only the ones a person set here. rev = the
-- device revision that last changed it (secrets are re-sent by revision).
CREATE TABLE device_settings (
  device_id  INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key        TEXT NOT NULL,
  value      TEXT NOT NULL DEFAULT '',
  rev        INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (device_id, key)
);
CREATE INDEX device_settings_user ON device_settings(user_id, device_id);

-- Commands queued for a device, delivered at most once.
CREATE TABLE device_commands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id  INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  args       TEXT NOT NULL DEFAULT '{}',
  state      TEXT NOT NULL DEFAULT 'queued',     -- queued | sent | done | failed | cancelled
  result     TEXT NOT NULL DEFAULT '',
  actor      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  sent_at    TEXT NOT NULL DEFAULT '',
  done_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX device_commands_device ON device_commands(user_id, device_id, id);
CREATE INDEX device_commands_queue ON device_commands(device_id, state);

-- Who changed what, when: settings, commands, tokens, refusals, offline.
CREATE TABLE device_log (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  actor     TEXT NOT NULL DEFAULT '',            -- a person's name, "device", or a Pocket app
  action    TEXT NOT NULL,                       -- added | setting | reset | command | token | refused | device_changed | offline | online | renamed
  key       TEXT NOT NULL DEFAULT '',
  old_value TEXT NOT NULL DEFAULT '',
  new_value TEXT NOT NULL DEFAULT '',
  detail    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX device_log_device ON device_log(user_id, device_id, id);

-- A small history of what devices report (one row per device per 5 minutes).
CREATE TABLE device_samples (
  device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  at        TEXT NOT NULL,                       -- start of the 5-minute bucket, UTC
  rssi      INTEGER,
  temp_c    REAL,
  PRIMARY KEY (device_id, at)
);
CREATE INDEX device_samples_user ON device_samples(user_id, device_id, at);
