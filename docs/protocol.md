# NTP Clock: server protocol (version 1)

This is the contract between the NTP Clock firmware and Pocket Devices, as
given with the firmware (`server-protocol.md`). Pocket Devices implements it
unchanged; the notes at the end say how the server reads the few points the
contract leaves open. The setting table below lives as data in
`internal/kinds/ntp-clock.json` (validator and web UI read the same file).

The clock polls a server over HTTPS. The server keeps the *desired* settings
of every clock, the clock applies them and reports what it really has. The
clock always opens the connection (it may sit behind NAT), the server never
connects to the clock. Everything that can be set on the serial console can be
set through this protocol; the console stays as it is.

## Transport and identity

- HTTPS only, certificate checked against the public CA bundle of ESP-IDF
  (no self-signed certificates). TLS 1.2 or better.
- Base URL = the clock setting `server_url`, for example
  `https://clock.example.com` (no trailing slash).
- Device id: the WiFi MAC address, 12 upper case hex digits, e.g. `A1B2C3D4E5F6`.
- Authentication: header `Authorization: Bearer <device_token>`. The token is a
  random secret of 32 to 64 characters, one per device, created on the server
  and typed into the clock (`cfg set device_token ...`). The server stores
  only a hash. A wrong token gives `401`.
- Bodies are JSON (`Content-Type: application/json`, UTF-8), at most 8 KiB.

## The one endpoint: `POST /api/v1/device/sync`

The clock calls it every `poll_s` seconds (default 30, the server may change
it in every answer, limits 10 .. 3600) and also right after it starts.

### Request (clock to server)

```json
{
  "device_id": "A1B2C3D4E5F6",
  "fw": "0.1.0",
  "reset_reason": "power-on",
  "uptime_s": 86412,
  "heap_free": 181076,
  "display": "seg7",
  "wifi": { "ssid": "Apolyx", "rssi": -52, "channel": 10, "ip": "192.168.100.68" },
  "time": { "synced": true, "unix": 1764000000, "last_sync_age_s": 1200 },
  "weather": { "place": "Banja Luka", "temp_c": 8.4 },
  "alarm": { "ringing": false },
  "config_rev": 17,
  "config": { "ssid": "Apolyx", "pass": "****", "tz": "CET-1CEST,M3.5.0,M10.5.0/3" },
  "config_errors": { "alarm_time": "out of range" },
  "command_results": [ { "id": "c-42", "ok": true, "msg": "" } ]
}
```

- `config` has every setting as a string, exactly as the console shows it (see
  the table below). Secrets (`pass`, `device_token`) are sent as `"****"`.
- `config_rev` is the revision of the desired settings that the clock applied
  last (0 = none yet).
- `config_errors` lists settings of the last answer that the clock refused
  (key to short reason), absent when there were none.
- `command_results` reports the commands of the last answer when the clock
  could (a reboot cannot report itself), absent when empty.
- `time.unix`, `weather` and `alarm` are only present when known.

### Response (server to clock)

```json
{
  "config_rev": 18,
  "config": { "brightness": "60", "alarm": "on", "alarm_time": "06:30" },
  "commands": [ { "id": "c-43", "name": "reboot" } ],
  "poll_s": 30,
  "time": { "unix": 1764000012, "tz": "CET-1CEST,M3.5.0,M10.5.0/3" }
}
```

- `config_rev`: revision number of the desired settings of this device on the
  server, grows with every change made in the web interface. If it equals the
  `config_rev` of the request, `config` is omitted.
- `config`: the settings to apply, key to string value, **only the ones that
  differ** from the `config` of the request (the server compares), or all of
  them. Values are in console form (`on`/`off`, `HH:MM`, `mon-fri`, `seg7`,
  `c`, decimal degrees). The clock validates every value with the same rules as
  the console and keeps the old value when it refuses one.
- `commands`: things to do now, in order. Delivery is *at most once*: the server
  removes a command from its queue when it sends it.
- `poll_s`: next poll interval (optional).
- `time`: optional, the server's clock (phase 2: the clock can use it instead
  of NTP). `tz` is a POSIX TZ string.
- Unknown fields must be ignored on both sides. Status codes: `200` answer,
  `401` wrong token, `404` unknown device, `429` slow down (the clock backs off
  and honours `Retry-After`), `5xx` the clock retries with a growing delay up
  to 5 minutes.

### Commands

| Name | Argument | What the clock does |
|---|---|---|
| `reboot` | | restarts after the answer |
| `setup_mode` | | restarts into the setup mode (own WiFi network) |
| `sync_time` | | asks the NTP server now |
| `weather_refresh` | | fetches the temperature now |
| `alarm_stop` | | stops a ringing alarm |
| `alarm_test` | `"n": 0..6` | plays an alarm sound |
| `cfg_reset` | | erases all settings (dangerous, the web interface must ask twice) |

Unknown commands are answered with `ok: false`.

### Rules the clock follows

- These settings are never changed by the server (the clock ignores them and
  reports a `config_errors` entry): `remote`, `server_url`, `device_token`.
- `"****"` as a value of a secret setting means "unchanged" and is ignored.
- A setting is applied with the console rules, so the clock stays consistent;
  some need a restart to take effect (`display`), the server should offer a
  reboot after changing them.
- Changing `ssid` or `pass` may cut the connection. The clock then falls back
  to its setup mode after 3 refused logins (`ap_fallback_n`), the server does
  not need to handle it, but the web interface should warn.

## Settings (the keys of `config`)

Type: `text`, `number` (integer with range), `bool` (`on`/`off`), `hhmm`
(`HH:MM`), `days`, `enum`, `coord` (decimal degrees, 6 decimals).

| Key | Type | Range / values | Default | Meaning |
|---|---|---|---|---|
| `ssid` | text | 1 .. 32 | empty | WiFi network |
| `pass` | text (secret) | 0 or 8 .. 64 | empty | WiFi password |
| `hostname` | text | 1 .. 32 | `apx-ntp-clock` | DHCP host name |
| `ntp_server` | text | 1 .. 64 | `pool.ntp.org` | NTP server |
| `ntp_interval` | number | 15 .. 1440 | 60 | NTP resync, minutes |
| `sync_timeout` | number | 15 .. 10080 | 180 | minutes without sync until "stale" |
| `tz` | text | POSIX TZ, 1 .. 64 | `UTC0` | time zone, e.g. `CET-1CEST,M3.5.0,M10.5.0/3` |
| `fmt24h` | bool | | on | 24 or 12 hour |
| `show_seconds` | bool | | on | show seconds |
| `leading_zero` | bool | | on | `07` or ` 7` |
| `date_fmt` | number | 0 = DD.MM.YY, 1 = MM.DD.YY, 2 = YY.MM.DD | 0 | date order |
| `display` | enum | `seg7`, `lcd` | `seg7` | display type (needs a reboot) |
| `splash_text` | text | 0 .. 64; on the 7-segment display only characters it can show | `HELLO` | boot text |
| `splash_secs` | number | 1 .. 600 | 5 | minimum splash time |
| `scroll_ms` | number | 50 .. 2000 | 300 | scroll step |
| `clock_secs` | number | 1 .. 3600 | 30 | clock page time (7 segment) |
| `date_secs` | number | 0 .. 3600 | 5 | date page, 0 = never |
| `temp_secs` | number | 0 .. 3600 | 3 | temperature page, 0 = never |
| `alarm_page_secs` | number | 0 .. 3600 | 3 | alarm time page, 0 = never |
| `temp_unit` | enum | `c`, `f` | `c` | unit |
| `brightness` | number | 1 .. 100 | 100 | manual brightness % |
| `bright_auto` | bool | | on | brightness from the light sensor |
| `bright_min` | number | 1 .. 100 | 10 | lowest auto brightness % |
| `ldr_dark_mv` | number | 1 .. 3300 | 60 | sensor voltage of the lowest level |
| `ldr_bright_mv` | number | 2 .. 3300 | 1800 | sensor voltage of the highest level |
| `alarm` | bool | | off | alarm armed |
| `alarm_time` | hhmm | 00:00 .. 23:59 | 07:00 | wake-up time |
| `alarm_days` | days | `all`, `weekdays`, `weekend`, `none`, list `mon,wed,fri`, range `mon-fri` | `all` | days |
| `alarm_secs` | number | 1 .. 600 | 60 | ring time |
| `alarm_sound` | number | 0 .. 6 | 0 | sound pattern |
| `chime` | bool | | on | beep every full hour |
| `status_beep` | bool | | on | beeps for WiFi, sync |
| `quiet_from` | hhmm | | 22:00 | quiet hours start (chime and beeps) |
| `quiet_to` | hhmm | | 07:00 | quiet hours end |
| `place` | text | 0 .. 47 | empty | weather place name |
| `lat` | coord | -90 .. 90 | 0 | latitude |
| `lon` | coord | -180 .. 180 | 0 | longitude |
| `weather_poll` | number | 5 .. 1440 | 30 | weather fetch, minutes |
| `wifi_tx` | number | 2 .. 20 | 20 | WiFi power, dBm |
| `ap_fallback_n` | number | 0 .. 100 | 3 | refused logins before the setup mode, 0 = never |
| `log` | bool | | off | log lines on the serial console |
| `remote` | bool | | off | the clock talks to the server (never changed by it) |
| `server_url` | text | https URL, 1 .. 96 | empty | never changed by the server |
| `device_token` | text (secret) | 32 .. 64 | empty | never changed by the server |
| `remote_poll_s` | number | 10 .. 3600 | 30 | default poll interval when the server gives none |

The authoritative list of keys and rules is `app/cfg/src/cfg.c`; the server
keeps its own copy of this table (key, type, range, default, group, label,
help) as data, so a new key needs no code change. The clock groups the keys
for `cfg list` as: WiFi, Time, Display, Pages and splash, Weather, Alarm,
Chime and beeps, System, Server.

---

## How Pocket Devices reads the open points

No protocol field was added or changed. Where the contract leaves a choice,
the server does this:

- **Command arguments** travel as fields of the command object:
  `{ "id": "c-44", "name": "alarm_test", "n": 3 }`. Command ids are `c-<n>`.
- **Which settings it sends.** Only settings set in the web interface are
  "desired"; a fresh clock keeps everything it has. With a new revision the
  answer holds the desired values that differ from the request's `config`
  (compared by meaning: `7:00` = `07:00`, `44.7722` = `44.772200`), plus the
  secrets changed after the revision the clock reported (a secret is only ever
  `****` in the request, so it cannot be compared). When the revisions differ
  but nothing differs, `config` is sent as `{}` so the clock can take the new
  revision.
- **A change made at the clock** (console, setup page) while the revisions are
  equal wins: the server takes the reported value as the new desired one and
  logs it, so a later web change does not quietly undo it. A refused value
  (`config_errors`) is handled the same way, with the clock's reason in the log.
- **After `cfg_reset`** the clock reports revision 0 and gets every desired
  setting again, the WiFi password included.
- **A clock ahead of the server** (a restored backup): the server continues from
  the clock's revision number.
- **Lost answers and double polls** are harmless: the same request gets the same
  `config` again. Commands are the exception, at most once by design.
- **`time`** is always sent: `unix` now, `tz` the desired time zone (else the
  reported one).
- **Online** = seen within three `poll_s` intervals plus 15 seconds.
- **Rate limits**: per device a bucket of 6 calls refilled one every 5 seconds;
  per client address, 20 failed tokens or unknown devices in 10 minutes. Both
  answer `429` with `Retry-After`.
- **Sizes**: a request over 8 KiB gets `413`; answers stay well under 8 KiB
  (at most 10 commands per answer, the rest follow in the next ones). The
  answer has a `Content-Length`, no gzip, no redirects.
- **Validation** is the table above, a little stricter where the clock's exact
  rule is unknown: `hostname` takes letters, digits and hyphens;
  `ntp_server` a host name; `splash_text` printable ASCII; `tz` must look like a
  POSIX TZ string; `alarm_days` is one keyword, one range or a list (no mixed
  `mon-wed,fri`). Never more permissive than the console.

### Open questions for the firmware

- The groups of `wifi_tx`, `ap_fallback_n` and `temp_unit` are guessed (WiFi,
  WiFi, Weather); `cfg.c` is authoritative.
- Which characters the 7-segment display can show for `splash_text`.
- Whether the clock's TZ parser takes `<+03>-3` style names (the built-in time
  zone list avoids them).
