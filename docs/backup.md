# Backup

One SQLite file: `DATA_DIR/pocket-devices.db` (WAL mode). Take a consistent copy with `pocket-devices backup` (writes `DATA_DIR/backups/pocket-devices-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin). Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-devices.db` (remove `-wal`/`-shm` files), start.

Every person can also export their own data as JSON (`/api/v1/export.json`, re-importable) or the list of devices as CSV. Device tokens are stored as hashes, so a restored backup (or an export imported on another box) keeps every device working.
