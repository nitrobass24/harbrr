-- 0022_drop_proxy_url_encrypted.sql — drop the legacy proxies.url_encrypted
-- column now that every proxy row is guaranteed split into structured fields
-- (#71, #294).
--
-- 0015 added structured host/port/username/password_encrypted to proxies but kept
-- the legacy composite url_encrypted (NOT NULL, default '') for the boot backfill's
-- window (internal/resourcemigrate.SplitProxyURLs, removed in this same PR now that
-- this migration makes the backfill a permanent no-op — see the guard below). That
-- column is dropped here.
--
-- Guard: refuse to apply while any row still has url_encrypted != '' (an un-split
-- row — the backfill clears it back to '' in the same write that sets host, and
-- every post-#71 insert already writes ''). The boot order (internal/app/app.go:
-- db.Migrate fully succeeds before SplitProxyURLs ever runs, and a Migrate error is
-- fatal) means this migration always runs either against a fresh DB or against a DB
-- where a prior boot's backfill has already completed — so by the time this guard
-- passes, dropping the column is permanently safe and SplitProxyURLs becomes dead
-- code (removed in this PR).
--
-- Unlike 0021's app tables, this is a plain DROP COLUMN, not a stage/drop/rename
-- rebuild: url_encrypted participates in no index or constraint (0012's plain
-- schema), so SQLite can drop it directly.
--
-- This drop purges nothing new: the backfill already clears a split row's
-- url_encrypted back to '' as part of splitting it, so every row already reaching
-- this migration either never held ciphertext (post-#71 inserts) or has had its
-- ciphertext cleared (backfilled rows) — the column is empty on every surviving row
-- before this migration ever runs.
CREATE TEMP TABLE _0022_guard (x INTEGER);

CREATE TEMP TRIGGER _0022_guard_check
AFTER INSERT ON _0022_guard
BEGIN
	SELECT RAISE(ABORT, 'harbrr: cannot apply migration 0022 - one or more proxies have not yet been split into structured host/port/username/password fields; run the release immediately before this one once more (it retries the split on every boot and logs the outcome), confirm no more "splitting legacy proxy URLs" warnings appear, then upgrade to this version')
	WHERE (SELECT COUNT(*) FROM proxies WHERE url_encrypted != '') > 0;
END;

INSERT INTO _0022_guard (x) VALUES (1);

DROP TRIGGER _0022_guard_check;
DROP TABLE _0022_guard;

ALTER TABLE proxies DROP COLUMN url_encrypted;
