CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS sites (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL UNIQUE,
 auth_username TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','disabled')),
 current_release_id TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS releases (
 id TEXT PRIMARY KEY, site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(version > 0), storage_path TEXT NOT NULL, checksum TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('ready','active','superseded')), created_at INTEGER NOT NULL,
 UNIQUE(site_id, version)
);
CREATE UNIQUE INDEX IF NOT EXISTS releases_one_active ON releases(site_id) WHERE status='active';
CREATE TABLE IF NOT EXISTS admins (
 id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_sessions (
 id_hash TEXT PRIMARY KEY, created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS login_attempts (
 key TEXT PRIMARY KEY, count INTEGER NOT NULL, window_start INTEGER NOT NULL, locked_until INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS audit_events (
 id INTEGER PRIMARY KEY, actor TEXT NOT NULL, action TEXT NOT NULL, site_id TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_site ON audit_events(site_id, created_at);
INSERT OR IGNORE INTO schema_migrations(version) VALUES(1);
