-- Every kind of device reports its own readings (internal/kinds/*.json):
-- one generic table instead of the clock's temperature and signal columns.
CREATE TABLE device_readings (
  device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key       TEXT NOT NULL,                      -- the reading's key in the kind file
  at        TEXT NOT NULL,                      -- start of the 5-minute bucket, UTC
  value     REAL NOT NULL,
  PRIMARY KEY (device_id, key, at)
);
CREATE INDEX device_readings_user ON device_readings(user_id, device_id, key, at);
INSERT INTO device_readings (device_id, user_id, key, at, value) SELECT device_id, user_id, 'temp_c', at, temp_c FROM device_samples WHERE temp_c IS NOT NULL;
INSERT INTO device_readings (device_id, user_id, key, at, value) SELECT device_id, user_id, 'rssi', at, rssi FROM device_samples WHERE rssi IS NOT NULL;
DROP TABLE device_samples;

-- What devices tell as it happens: a button press, a door opening, motion.
-- ext_id is the device's own id for the event, so a resent event is stored once.
CREATE TABLE device_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id   INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  ext_id      TEXT NOT NULL DEFAULT '',
  type        TEXT NOT NULL,
  data        TEXT NOT NULL DEFAULT '{}',
  at          TEXT NOT NULL,                    -- when it happened (the device's clock, else when it arrived)
  received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX device_events_device ON device_events(user_id, device_id, id);
CREATE UNIQUE INDEX device_events_ext ON device_events(device_id, ext_id) WHERE ext_id != '';

-- Alerts that hold now (alert id → since), to tell the person only on a change.
ALTER TABLE devices ADD COLUMN alerts TEXT NOT NULL DEFAULT '{}';
