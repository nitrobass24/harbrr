-- 0012_proxies_solvers.sql — global, reusable proxy + anti-bot-solver resources.
--
-- Until now a proxy or a FlareSolverr endpoint was configured inline, per indexer
-- (the proxy_type/proxy_url and solver_type/flaresolverr_url/flaresolverr_max_timeout
-- settings). That meant re-entering the same endpoint on every tracker behind it.
-- These two tables make them named, shared resources an indexer references by id;
-- the per-tracker manual-cookie solver stays inline (it is genuinely per-tracker).
--
-- `type` is validated in Go, not by a DB CHECK, so a new proxy scheme or solver kind
-- needs no migration (the #85 lesson). The endpoint URL is the stored secret in both
-- (a proxy URL routinely embeds user:pass; a FlareSolverr URL may sit behind auth), so
-- it is base64 nonce‖ciphertext‖tag encrypted under key_id with the resource's own id
-- as AAD — exactly like a notification URL — and reads back <redacted> in the API.

CREATE TABLE proxies (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	type          TEXT NOT NULL,
	url_encrypted TEXT NOT NULL,
	key_id        TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE TABLE solvers (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	type          TEXT NOT NULL,
	url_encrypted TEXT NOT NULL,
	key_id        TEXT NOT NULL,
	max_timeout   INTEGER NOT NULL DEFAULT 0,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

-- An instance references at most one proxy and one solver; deleting the resource
-- nulls the reference (the indexer falls back to no proxy / no solver, never breaks).
-- ADD COLUMN with a REFERENCES clause is allowed because the default is NULL.
ALTER TABLE indexer_instances ADD COLUMN proxy_id  INTEGER REFERENCES proxies(id) ON DELETE SET NULL;
ALTER TABLE indexer_instances ADD COLUMN solver_id INTEGER REFERENCES solvers(id) ON DELETE SET NULL;
