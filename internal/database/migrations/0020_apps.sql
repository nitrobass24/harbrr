-- 0020_apps.sql — first-class App identity (ADR 0004). See docs/adr/0004-app-identity.md.
--
-- An App is a (kind, base_url) external service harbrr connects to, stored once with
-- one sealed credential + its harbrr vantage, and referenced by the three surface
-- tables via app_id. (0019 is claimed by an in-flight sibling PR; the gap is fine —
-- migrations apply in lexical order and these touch disjoint tables.)
CREATE TABLE apps (
    id                INTEGER PRIMARY KEY,
    kind              TEXT NOT NULL,               -- validated in Go, no CHECK (#85 lesson)
    name              TEXT NOT NULL,
    base_url          TEXT NOT NULL,
    username          TEXT NOT NULL DEFAULT '',    -- empty for API-key apps; set for user+password
    api_key_encrypted TEXT NOT NULL DEFAULT '',    -- the app's credential (API key OR password)
    key_id            TEXT NOT NULL DEFAULT '',
    harbrr_url        TEXT NOT NULL DEFAULT '',     -- how this app reaches harbrr's feed
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (kind, base_url)
);

-- Surface tables gain app_id (nullable; the boot fold populates it). The legacy
-- identity/credential columns are kept for the fold window and dropped by a later
-- cleanup migration — the 0015 proxysplit precedent.
ALTER TABLE app_connections      ADD COLUMN app_id INTEGER REFERENCES apps(id);
ALTER TABLE announce_connections ADD COLUMN app_id INTEGER REFERENCES apps(id);
ALTER TABLE download_clients     ADD COLUMN app_id INTEGER REFERENCES apps(id);
