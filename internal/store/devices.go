package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-devices/internal/kinds"
)

const deviceCols = `id, user_id, kind, name, hw_id, location, notes, token_hash, token_hint, token_at, poll_s, desired_rev, applied_rev,
	first_seen_at, last_seen_at, last_ip, status, reported, config_errors, online, notify_offline, demo, created_at, updated_at, alerts`

type scanner interface{ Scan(...any) error }

func scanDevice(r scanner) (*Device, error) {
	var d Device
	var status, reported, errs, alerts string
	var online, notifyOff, demo int
	if err := r.Scan(&d.ID, &d.UserID, &d.Kind, &d.Name, &d.HWID, &d.Location, &d.Notes, &d.TokenHash, &d.TokenHint, &d.TokenAt, &d.PollS,
		&d.DesiredRev, &d.AppliedRev, &d.FirstSeenAt, &d.LastSeenAt, &d.LastIP, &status, &reported, &errs, &online, &notifyOff, &demo,
		&d.CreatedAt, &d.UpdatedAt, &alerts); err != nil {
		return nil, notFound(err)
	}
	d.Status = json.RawMessage(status)
	if !json.Valid(d.Status) {
		d.Status = json.RawMessage("{}")
	}
	d.Reported = map[string]string{}
	_ = json.Unmarshal([]byte(reported), &d.Reported)
	d.ConfigErrors = map[string]string{}
	_ = json.Unmarshal([]byte(errs), &d.ConfigErrors)
	d.Alerts = map[string]string{}
	_ = json.Unmarshal([]byte(alerts), &d.Alerts)
	d.Online, d.NotifyOffline, d.Demo = online == 1, notifyOff == 1, demo == 1
	return &d, nil
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// CreateDevice adds a device; a hardware id already on the box is a conflict.
func (s *Store) CreateDevice(ctx context.Context, uid int64, d *Device, actor string) error {
	if d.PollS == 0 {
		d.PollS = 30
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO devices (user_id, kind, name, hw_id, location, notes, token_hash, token_hint, poll_s, notify_offline, demo)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, d.Kind, d.Name, d.HWID, d.Location, d.Notes, d.TokenHash, d.TokenHint, d.PollS, boolInt(d.NotifyOffline), boolInt(d.Demo))
	if err != nil {
		return conflict(err)
	}
	d.ID, _ = res.LastInsertId()
	_ = s.AddLog(ctx, uid, d.ID, LogEntry{Actor: actor, Action: "added", Detail: d.Name})
	got, err := s.GetDevice(ctx, uid, d.ID)
	if err == nil {
		*d = *got
	}
	return err
}

// GetDevice returns one of a person's devices.
func (s *Store) GetDevice(ctx context.Context, uid, id int64) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE user_id = ? AND id = ?`, uid, id))
}

// DeviceByID returns a device whoever owns it (background work only).
func (s *Store) DeviceByID(ctx context.Context, id int64) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

// DeviceByHWID finds a device by the id it sends (the device endpoint; no person yet).
func (s *Store) DeviceByHWID(ctx context.Context, hwid string) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE hw_id = ?`, hwid))
}

func (s *Store) listDevices(ctx context.Context, q string, args ...any) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListDevices lists a person's devices by name.
func (s *Store) ListDevices(ctx context.Context, uid int64) ([]Device, error) {
	return s.listDevices(ctx, `WHERE user_id = ? ORDER BY name COLLATE NOCASE, id`, uid)
}

// OnlineDevices lists every device last known online (the offline check).
func (s *Store) OnlineDevices(ctx context.Context) ([]Device, error) {
	return s.listDevices(ctx, `WHERE online = 1 ORDER BY id`)
}

// DemoDevices lists the simulated devices.
func (s *Store) DemoDevices(ctx context.Context) ([]Device, error) {
	return s.listDevices(ctx, `WHERE demo = 1 ORDER BY id`)
}

// UpdateDevice saves the name, place, notes, check-in interval and the offline alert.
func (s *Store) UpdateDevice(ctx context.Context, uid int64, d *Device) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE devices SET name = ?, location = ?, notes = ?, poll_s = ?, notify_offline = ?, updated_at = ? WHERE user_id = ? AND id = ?`,
		d.Name, d.Location, d.Notes, d.PollS, boolInt(d.NotifyOffline), Now(), uid, d.ID)
	return affected(res, err)
}

// SetDeviceToken replaces the token hash.
func (s *Store) SetDeviceToken(ctx context.Context, uid, id int64, hash, hint string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE devices SET token_hash = ?, token_hint = ?, token_at = ?, updated_at = ? WHERE user_id = ? AND id = ?`, hash, hint, Now(), Now(), uid, id)
	return affected(res, err)
}

// SetOnline records the last known online state.
func (s *Store) SetOnline(ctx context.Context, id int64, online bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE devices SET online = ? WHERE id = ?`, boolInt(online), id)
	return err
}

// DeleteDevice removes a device and everything about it.
func (s *Store) DeleteDevice(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE user_id = ? AND id = ?`, uid, id)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- desired settings ----

// Desired returns the values set here for a device.
func (s *Store) Desired(ctx context.Context, uid, deviceID int64) (map[string]DesiredSetting, error) {
	return readDesired(ctx, s.DB, `SELECT key, value, rev, updated_at FROM device_settings WHERE user_id = ? AND device_id = ?`, uid, deviceID)
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readDesired(ctx context.Context, q querier, query string, args ...any) (map[string]DesiredSetting, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DesiredSetting{}
	for rows.Next() {
		var d DesiredSetting
		if err := rows.Scan(&d.Key, &d.Value, &d.Rev, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out[d.Key] = d
	}
	return out, rows.Err()
}

// SetDesired stores new desired values (already validated) as one new
// revision and logs each change. Values equal to the current desired one are
// skipped; when nothing changes the revision stays. Returns the revision.
func (s *Store) SetDesired(ctx context.Context, uid, deviceID int64, k *kinds.Kind, values map[string]string, actor, action string) (int64, []string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	var rev int64
	var reportedJSON string
	if err := tx.QueryRowContext(ctx, `SELECT desired_rev, reported FROM devices WHERE user_id = ? AND id = ?`, uid, deviceID).Scan(&rev, &reportedJSON); err != nil {
		return 0, nil, notFound(err)
	}
	reported := map[string]string{}
	_ = json.Unmarshal([]byte(reportedJSON), &reported)
	cur, err := readDesired(ctx, tx, `SELECT key, value, rev, updated_at FROM device_settings WHERE device_id = ?`, deviceID)
	if err != nil {
		return 0, nil, err
	}
	next := rev + 1
	now := Now()
	var changed []string
	for key, v := range values {
		st, _ := k.Setting(key)
		old, had := cur[key]
		if had && old.Value == v && !st.Secret {
			continue
		}
		before := old.Value
		if !had {
			before = reported[key]
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_settings (device_id, user_id, key, value, rev, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(device_id, key) DO UPDATE SET value = excluded.value, rev = excluded.rev, updated_at = excluded.updated_at`, deviceID, uid, key, v, next, now); err != nil {
			return 0, nil, err
		}
		shownOld, shownNew := before, v
		if st.Secret {
			shownOld, shownNew = "", "(new value)"
			if before != "" && before != kinds.SecretMask {
				shownOld = "(old value)"
			}
		}
		if err := addLog(ctx, tx, uid, deviceID, LogEntry{Actor: actor, Action: action, Key: key, Old: shownOld, New: shownNew}); err != nil {
			return 0, nil, err
		}
		changed = append(changed, key)
	}
	if len(changed) == 0 {
		return rev, nil, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET desired_rev = ?, updated_at = ? WHERE id = ?`, next, now, deviceID); err != nil {
		return 0, nil, err
	}
	return next, changed, tx.Commit()
}

// Diff is what a device still has to apply: the desired values that differ
// from what it reports, and secrets changed after the revision it applied.
// Locked settings are never sent.
func Diff(k *kinds.Kind, desired map[string]DesiredSetting, reported map[string]string, deviceRev int64) map[string]string {
	out := map[string]string{}
	for key, d := range desired {
		st, ok := k.Setting(key)
		if !ok || st.Locked {
			continue
		}
		if st.Secret {
			if d.Rev > deviceRev {
				out[key] = d.Value
			}
			continue
		}
		if rv, ok := reported[key]; !ok || !k.Equal(key, rv, d.Value) {
			out[key] = d.Value
		}
	}
	return out
}

// ---- the sync transaction ----

// CommandResult is a device's answer to one command.
type CommandResult struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

// SyncIn is one report of a device.
type SyncIn struct {
	Status       json.RawMessage
	Config       map[string]string // nil = not sent
	ConfigRev    int64
	ConfigErrors map[string]string
	Results      []CommandResult
	IP           string
	Readings     map[string]float64 // the kind's chart readings, kept in 5-minute buckets
	Events       []EventIn
	Now          time.Time
	MaxCommands  int
}

// SyncOut is what the server answers and what happened.
type SyncOut struct {
	ConfigRev  int64
	Config     map[string]string // nil = in step, omit
	Commands   []Command
	CameOnline bool              // seen again after being marked offline
	FirstSeen  bool              // the very first check-in
	Refused    map[string]string // refusals not reported before
	PollS      int
	TZ         string
	NewEvents  []Event // events stored now for the first time
}

// EventIn is one event a device sent.
type EventIn struct {
	ExtID string
	Type  string
	Data  map[string]any
	At    time.Time
}

// Sync stores a report and works out the answer, in one transaction: the
// device may poll twice or lose an answer, so everything is idempotent
// except command delivery, which is at most once by design.
func (s *Store) Sync(ctx context.Context, d *Device, k *kinds.Kind, in SyncIn) (*SyncOut, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cur, err := scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, d.ID))
	if err != nil {
		return nil, err
	}
	now := in.Now.UTC().Format(time.RFC3339Nano)
	out := &SyncOut{PollS: cur.PollS, Refused: map[string]string{}}

	// command results
	for _, r := range in.Results {
		n, err := strconv.ParseInt(strings.TrimPrefix(r.ID, "c-"), 10, 64)
		if err != nil || !strings.HasPrefix(r.ID, "c-") {
			continue
		}
		state := "done"
		if !r.OK {
			state = "failed"
		}
		msg := r.Msg
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_commands SET state = ?, result = ?, done_at = ? WHERE id = ? AND device_id = ? AND state = 'sent'`, state, msg, now, n, cur.ID); err != nil {
			return nil, err
		}
	}

	reported := cur.Reported
	if in.Config != nil {
		reported = in.Config
	}
	errs := in.ConfigErrors
	if errs == nil {
		errs = map[string]string{}
	}
	for key, why := range errs {
		if cur.ConfigErrors[key] != why {
			out.Refused[key] = why
		}
	}
	desired, err := readDesired(ctx, tx, `SELECT key, value, rev, updated_at FROM device_settings WHERE device_id = ?`, cur.ID)
	if err != nil {
		return nil, err
	}
	rev := cur.DesiredRev
	if in.ConfigRev > rev {
		// the device is ahead (a restored backup, another server): continue from its number
		rev = in.ConfigRev
	}
	if in.ConfigRev >= rev {
		// in step: what the device has now wins (a console change, a refusal)
		for key, dv := range desired {
			st, ok := k.Setting(key)
			if !ok || st.Locked || st.Secret {
				continue
			}
			rv, ok := reported[key]
			if !ok || k.Equal(key, rv, dv.Value) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE device_settings SET value = ?, updated_at = ? WHERE device_id = ? AND key = ?`, rv, now, cur.ID, key); err != nil {
				return nil, err
			}
			action, detail := "device_changed", "changed at the device"
			if why, refused := errs[key]; refused {
				action, detail = "refused", why
			}
			if err := addLog(ctx, tx, cur.UserID, cur.ID, LogEntry{Actor: "device", Action: action, Key: key, Old: dv.Value, New: rv, Detail: detail}); err != nil {
				return nil, err
			}
		}
	} else {
		out.Config = Diff(k, desired, reported, in.ConfigRev)
	}
	out.ConfigRev = rev
	for key, why := range out.Refused {
		if _, logged := desired[key]; logged && in.ConfigRev >= rev {
			continue // logged above with the value
		}
		if err := addLog(ctx, tx, cur.UserID, cur.ID, LogEntry{Actor: "device", Action: "refused", Key: key, Detail: why}); err != nil {
			return nil, err
		}
	}
	if v, ok := desired["tz"]; ok {
		out.TZ = v.Value
	} else {
		out.TZ = reported["tz"]
	}

	// commands, at most once
	limit := in.MaxCommands
	if limit <= 0 {
		limit = 10
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, user_id, device_id, name, args, state, result, actor, created_at, sent_at, done_at
		FROM device_commands WHERE device_id = ? AND state = 'queued' ORDER BY id LIMIT ?`, cur.ID, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Commands = append(out.Commands, *c)
	}
	rows.Close()
	for _, c := range out.Commands {
		if _, err := tx.ExecContext(ctx, `UPDATE device_commands SET state = 'sent', sent_at = ? WHERE id = ?`, now, c.ID); err != nil {
			return nil, err
		}
	}

	out.FirstSeen = cur.FirstSeenAt == ""
	out.CameOnline = !cur.Online && !out.FirstSeen
	first := cur.FirstSeenAt
	if first == "" {
		first = now
		if err := addLog(ctx, tx, cur.UserID, cur.ID, LogEntry{Actor: "device", Action: "online", Detail: "first check-in"}); err != nil {
			return nil, err
		}
	}
	status := in.Status
	if len(status) == 0 || !json.Valid(status) {
		status = json.RawMessage("{}")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET status = ?, reported = ?, config_errors = ?, applied_rev = ?, desired_rev = ?,
		last_seen_at = ?, first_seen_at = ?, last_ip = ?, online = 1 WHERE id = ?`,
		string(status), mustJSON(reported), mustJSON(errs), in.ConfigRev, rev, now, first, in.IP, cur.ID); err != nil {
		return nil, err
	}
	bucket := in.Now.UTC().Truncate(5 * time.Minute).Format(time.RFC3339)
	for key, v := range in.Readings {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO device_readings (device_id, user_id, key, at, value) VALUES (?, ?, ?, ?, ?)`,
			cur.ID, cur.UserID, key, bucket, v); err != nil {
			return nil, err
		}
	}
	for _, e := range in.Events {
		at := e.At
		if at.IsZero() || at.After(in.Now.Add(time.Minute)) {
			at = in.Now
		}
		data := e.Data
		if data == nil {
			data = map[string]any{}
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO device_events (user_id, device_id, ext_id, type, data, at) VALUES (?, ?, ?, ?, ?, ?)`,
			cur.UserID, cur.ID, e.ExtID, e.Type, mustJSON(data), at.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			id, _ := res.LastInsertId()
			out.NewEvents = append(out.NewEvents, Event{ID: id, DeviceID: cur.ID, Type: e.Type, Data: data, At: at.UTC().Format(time.RFC3339Nano)})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- commands ----

const commandCols = `id, user_id, device_id, name, args, state, result, actor, created_at, sent_at, done_at`

func scanCommand(r scanner) (*Command, error) {
	var c Command
	var args string
	if err := r.Scan(&c.ID, &c.UserID, &c.DeviceID, &c.Name, &args, &c.State, &c.Result, &c.Actor, &c.CreatedAt, &c.SentAt, &c.DoneAt); err != nil {
		return nil, notFound(err)
	}
	c.Args = json.RawMessage(args)
	if !json.Valid(c.Args) {
		c.Args = json.RawMessage("{}")
	}
	return &c, nil
}

// QueueCommand queues a command for a device.
func (s *Store) QueueCommand(ctx context.Context, uid, deviceID int64, name string, args map[string]any, actor string) (*Command, error) {
	if args == nil {
		args = map[string]any{}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE user_id = ? AND id = ?`, uid, deviceID).Scan(&one); err != nil {
		return nil, notFound(err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO device_commands (user_id, device_id, name, args, actor) VALUES (?, ?, ?, ?, ?)`, uid, deviceID, name, mustJSON(args), actor)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	detail := ""
	if len(args) > 0 {
		detail = mustJSON(args)
	}
	if err := addLog(ctx, tx, uid, deviceID, LogEntry{Actor: actor, Action: "command", Key: name, Detail: detail}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return scanCommand(s.DB.QueryRowContext(ctx, `SELECT `+commandCols+` FROM device_commands WHERE id = ?`, id))
}

// ListCommands returns a device's latest commands, newest first.
func (s *Store) ListCommands(ctx context.Context, uid, deviceID int64, limit int) ([]Command, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+commandCols+` FROM device_commands WHERE user_id = ? AND device_id = ? ORDER BY id DESC LIMIT ?`, uid, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CancelCommand withdraws a command that has not been sent yet.
func (s *Store) CancelCommand(ctx context.Context, uid, deviceID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE device_commands SET state = 'cancelled', done_at = ? WHERE user_id = ? AND device_id = ? AND id = ? AND state = 'queued'`, Now(), uid, deviceID, id)
	return affected(res, err)
}

// QueuedCount is how many commands wait for a device.
func (s *Store) QueuedCount(ctx context.Context, deviceID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM device_commands WHERE device_id = ? AND state = 'queued'`, deviceID).Scan(&n)
	return n
}

// ---- log ----

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func addLog(ctx context.Context, x execer, uid, deviceID int64, e LogEntry) error {
	_, err := x.ExecContext(ctx, `INSERT INTO device_log (user_id, device_id, actor, action, key, old_value, new_value, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, deviceID, e.Actor, e.Action, e.Key, e.Old, e.New, e.Detail)
	return err
}

// AddLog adds a line to a device's history.
func (s *Store) AddLog(ctx context.Context, uid, deviceID int64, e LogEntry) error {
	return addLog(ctx, s.DB, uid, deviceID, e)
}

// ListLog returns a device's history, newest first (before = an id to page from).
func (s *Store) ListLog(ctx context.Context, uid, deviceID, before int64, limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, device_id, at, actor, action, key, old_value, new_value, detail FROM device_log WHERE user_id = ? AND device_id = ?`
	args := []any{uid, deviceID}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.At, &e.Actor, &e.Action, &e.Key, &e.Old, &e.New, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- readings and events ----

// Point is one 5-minute value of a reading.
type Point struct {
	At    string  `json:"at"`
	Value float64 `json:"v"`
}

// Readings returns a device's 5-minute points since a time, by reading key.
func (s *Store) Readings(ctx context.Context, uid, deviceID int64, since time.Time) (map[string][]Point, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, at, value FROM device_readings WHERE user_id = ? AND device_id = ? AND at >= ? ORDER BY key, at`,
		uid, deviceID, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Point{}
	for rows.Next() {
		var key string
		var p Point
		if err := rows.Scan(&key, &p.At, &p.Value); err != nil {
			return nil, err
		}
		out[key] = append(out[key], p)
	}
	return out, rows.Err()
}

// AddReading stores a point directly (demo history).
func (s *Store) AddReading(ctx context.Context, uid, deviceID int64, key string, at time.Time, v float64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO device_readings (device_id, user_id, key, at, value) VALUES (?, ?, ?, ?, ?)`,
		deviceID, uid, key, at.UTC().Truncate(5*time.Minute).Format(time.RFC3339), v)
	return err
}

// ListEvents returns a device's latest events, newest first.
func (s *Store) ListEvents(ctx context.Context, uid, deviceID, before int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT id, device_id, type, data, at FROM device_events WHERE user_id = ? AND device_id = ?`
	args := []any{uid, deviceID}
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Type, &data, &e.At); err != nil {
			return nil, err
		}
		e.Data = map[string]any{}
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddEvent stores an event directly (demo history).
func (s *Store) AddEvent(ctx context.Context, uid, deviceID int64, typ string, data map[string]any, at time.Time) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO device_events (user_id, device_id, type, data, at) VALUES (?, ?, ?, ?, ?)`, uid, deviceID, typ, mustJSON(data), at.UTC().Format(time.RFC3339Nano))
	return err
}

// SetAlerts stores the alerts that hold now (id → since).
func (s *Store) SetAlerts(ctx context.Context, deviceID int64, alerts map[string]string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE devices SET alerts = ? WHERE id = ?`, mustJSON(alerts), deviceID)
	return err
}

// Prune drops readings older than 30 days, events older than a year and old finished commands.
func (s *Store) Prune(ctx context.Context) error {
	cut := time.Now().UTC().AddDate(0, 0, -30)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM device_readings WHERE at < ?`, cut.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM device_events WHERE at < ?`, cut.AddDate(0, 0, -335).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM device_commands WHERE state IN ('done','failed','cancelled','sent') AND created_at < ?`, cut.AddDate(0, 0, -60).Format(time.RFC3339Nano))
	return err
}

// ImportDesired adds desired values without a new revision.
func (s *Store) ImportDesired(ctx context.Context, uid, deviceID int64, values map[string]DesiredSetting) error {
	for _, d := range values {
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO device_settings (device_id, user_id, key, value, rev) VALUES (?, ?, ?, ?, ?)`, deviceID, uid, d.Key, d.Value, d.Rev); err != nil {
			return err
		}
	}
	return nil
}

// String describes a device for logs.
func (d *Device) String() string { return fmt.Sprintf("%s %q (%s)", d.Kind, d.Name, d.HWID) }
