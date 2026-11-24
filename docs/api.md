# API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie, a personal token (`Authorization: Bearer pdev_…`, Settings → API & shortcuts), a Pocket Core token, or a Pocket Core link token (`pcx_…`, only the routes of its scope). Everything is per person: other people's ids answer 404.

The devices themselves use one endpoint with their own token: `POST /api/v1/device/sync`, see [protocol.md](protocol.md).

## Kinds of hardware

| | |
|---|---|
| `GET /kinds` | Every kind this server knows (clock, climate and air-quality sensors, button, door, motion and leak sensors, smart plug), with its full setting table (key, type, range, default, group, label, help, secret, locked, needs a restart), its commands, readings, alerts, events and the console lines that connect it. One JSON file per kind in `internal/kinds/`. |
| `GET /tz?zone=Europe/Sarajevo` | The POSIX time-zone string for an IANA zone: `{"tz":"CET-1CEST,M3.5.0,M10.5.0/3"}` |

## Devices

| | |
|---|---|
| `GET /devices` | Your devices with `state` (`online` / `offline` / `new`), `pending` (changes not taken yet), `queued` (commands to send), `readings` (the kind's readings, by key), `alerts` (those that hold now) and a `summary` (firmware, uptime, WiFi) |
| `GET /overview` | Counts: `total`, `online`, `offline`, `new`, `pending`, `alerts` — for Homepage |
| `POST /devices` | `{"kind":"climate-sensor","name":"Bathroom","hw_id":"A1:B2:C3:D4:E5:F6","location":"Bathroom"}` (`kind` is required) → the device plus `token` (shown only here) and `setup` (the console lines) |
| `GET /devices/{id}` | `{device, settings, server_url}`; `settings` is one row per key: `desired` (set here; never a secret), `reported` (what the device has; secrets as `****`), `pending`, `error` |
| `PUT /devices/{id}` | `name`, `location`, `notes`, `poll_s` (check-in interval handed to the device), `notify_offline` |
| `DELETE /devices/{id}` | Remove it and its history; its token stops working |
| `POST /devices/{id}/token` | A new token (the old one stops at once) with the console lines |
| `PUT /devices/{id}/settings` | `{"values":{"alarm":"on","alarm_time":"06:30"},"reset":["brightness"]}` — checked with the device's own rules; one wrong value and nothing is saved (`400 {"errors":{key: why}}`). Values may be strings, numbers or booleans. Settings the server never changes (`remote`, `server_url`, `device_token`) are refused. |
| `GET /devices/{id}/commands?limit` | Latest commands: `queued`, `sent`, `done`, `failed` (with the device's message), `cancelled` |
| `POST /devices/{id}/commands` | `{"name":"alarm_test","args":{"n":3}}`. Restart and setup mode need `"confirm":true`, erasing also `"confirm_again":true`. |
| `DELETE /devices/{id}/commands/{cid}` | Withdraw a command that was not sent yet |
| `GET /devices/{id}/log?before&limit` | History, newest first: who changed what, commands, refusals, offline/online |
| `GET /devices/{id}/readings?hours=48` | 5-minute points of the kind's chart readings: `{"temp_c":[{"at":…,"v":21.4}],…}` |
| `GET /devices/{id}/events?before&limit` | What the device told as it happened (presses, doors, motion), newest first |

## Pocket Weather

`GET /weather/places`, `GET /weather/search?q=` — your places in Pocket Weather (through Pocket Core or a direct token), to fill a clock's place, coordinates and time zone.

## Settings, people, data

`GET/PUT /settings`, `GET /settings/system`, `GET/POST/PUT/DELETE /users`, `GET/POST/DELETE /tokens`, `GET /backup` (admins: the whole database), `GET /export.json`, `GET /export.csv`, `POST /import`, `POST /integrations/test`, `POST /notify/test`.

## Pocket Core

Offers `devices:read` (devices, overview, history, readings, events; never WiFi passwords or tokens) and `devices:write` (settings and everyday commands; never restart, setup mode or erase). Uses Pocket Weather (`weather:read`). Events: `devices.connected`, `devices.went_offline`, `devices.back_online`, `devices.setting_refused`, `devices.event` (a press, a door, motion: `type`, `data`), `devices.alert`, `devices.alert_cleared`.
