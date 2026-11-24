# Configuration

Environment variables; every one has a default. Personal settings (notifications, connections) live in the UI per person — the `WEATHER_*`/`NOTIFY_*` values below are copied once into the first admin's settings. The address devices use (`server_url`) is set under Settings → General (empty = `BASE_URL`).

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in notifications, the OIDC callback, the default device address) |
| `DATA_DIR` | `./data` | Database, Core token, backups |
| `DB_PATH` | `$DATA_DIR/pocket-devices.db` | SQLite file |
| `MAX_BODY_BYTES` | `16777216` | Largest JSON request from the web UI, e.g. an export being imported (16 MB). The device endpoint has its own fixed 8 KiB limit. |
| `TZ` | – | Time zone of the server's logs |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `WEATHER_URL`, `WEATHER_TOKEN` | – | Pocket Weather, direct connection (or use Pocket Core): pick a clock's place |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | With `TRUST_PROXY` the device rate limit counts the real client address from `X-Forwarded-For` |
| `DEMO_DATA` | `false` | One simulated device of every kind in the owner's account, checking in live (screenshots, trying it out) |

Commands: `pocket-devices backup [-o FILE]`, `pocket-devices reset-password [-email] [-password]`, `pocket-devices simulate -kind KIND -url URL -id MAC -token TOKEN [-event press:action=double] [-random] [-once]` (behaves like a device of any kind), `pocket-devices version`.
