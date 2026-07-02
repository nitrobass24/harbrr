-- 0006_app_settings.sql — runtime-tunable global settings (key/value).
--
-- A small key/value store for OPERATIONAL settings an operator tunes at runtime via
-- the management API/UI without a restart (the first consumer is the search-cache
-- config: TTL tiers, thin threshold, refresh-ahead, enabled). It is deliberately
-- distinct from app_meta (schema/bootstrap metadata) so user-tunable config and
-- internal schema bookkeeping never share a namespace.
--
-- The config FILE remains the bootstrap/default source; a row here OVERRIDES the
-- file value at runtime (precedence: DB override > config file > hardcoded default).
-- Values are stored as TEXT (the consumer parses/validates its own types). No
-- secret ever lands here — secrets live in internal/secrets / encrypted columns.
-- updated_at is TEXT RFC3339 (UTC), the universal convention.
CREATE TABLE app_settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
