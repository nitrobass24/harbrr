-- 0011_notifications.sql — issue #97 notification targets.
--
-- A configured notification target harbrr fires operational events at: today an
-- indexer health failure (auth/anti-bot/rate-limited/parse), so an operator learns an
-- indexer broke without tailing logs. Two senders in the MVP — a generic webhook (JSON
-- POST) and a Discord webhook (embed) — distinguished by `type`, which is validated in
-- Go (no DB CHECK), so a new sender kind needs no migration (the lesson of #85).
--
-- The destination URL is the only stored secret: both a webhook and a Discord webhook
-- URL routinely embed a bearer token in the path, so it is base64 nonce‖ciphertext‖tag,
-- encrypted under key_id with the notification id as AAD — exactly like a connection's
-- api_key_encrypted. It is never logged and reads back as <redacted> in the API.
--
-- Event flags gate which triggers a target reacts to; on_health_failure is the only one
-- today and defaults ON, so a freshly-added target immediately surfaces indexer breakage.
CREATE TABLE notifications (
	id                INTEGER PRIMARY KEY,
	name              TEXT NOT NULL,
	type              TEXT NOT NULL,
	url_encrypted     TEXT NOT NULL,
	key_id            TEXT NOT NULL,
	enabled           INTEGER NOT NULL DEFAULT 1,
	on_health_failure INTEGER NOT NULL DEFAULT 1,
	created_at        TEXT NOT NULL,
	updated_at        TEXT NOT NULL
);
