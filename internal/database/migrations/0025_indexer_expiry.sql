-- 0025_indexer_expiry.sql — per-indexer VIP/membership expiry (#399).
--
-- Many private trackers time-box access or perks, and the failure is SILENT: when VIP
-- lapses every request still succeeds, it just costs ratio again. Nothing in harbrr's
-- health classification can see that, so the date has to be operator-entered.
--
-- Three operator-facing columns, all defaulting to "untracked" so an existing instance
-- behaves exactly as before:
--   expires_at      calendar date 'YYYY-MM-DD' ('' = untracked). A DATE, not an instant:
--                   stored as text and compared as a date, so no timezone ever shifts it.
--   expiry_kind     'perk' (VIP lapses, account survives) | 'account' (access ends) | ''.
--                   One date with a type — not two subsystems.
--   expiry_lifetime never-expires; wins over expires_at and never notifies.
--
-- Two bookkeeping columns, written only by the expiry scan (internal/notify/expiry.go)
-- and never surfaced in the API. They are the exactly-once + re-arm ledger:
--   expiry_notified_for   the expiry date the fired state belongs to ('' = nothing fired)
--   expiry_notified_days  the most urgent lead-time threshold already fired for that date
--
-- Keying the ledger on the DATE is what makes renewal re-arm for free: change expires_at
-- and expiry_notified_for no longer matches, so every threshold is armed again with no
-- reset write anywhere in the update path. And because the stored value is the most
-- urgent threshold fired (thresholds only ever get more urgent as the date approaches),
-- one row covers the whole ladder and survives a restart — no per-threshold table, and
-- a harbrr that was offline across several thresholds sends one message, not a backlog.
--
-- notifications.on_expiry is the per-target opt-in for the new event kind, defaulting ON
-- like on_health_failure so an existing target starts surfacing expiries immediately.
ALTER TABLE indexer_instances ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE indexer_instances ADD COLUMN expiry_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE indexer_instances ADD COLUMN expiry_lifetime INTEGER NOT NULL DEFAULT 0;
ALTER TABLE indexer_instances ADD COLUMN expiry_notified_for TEXT NOT NULL DEFAULT '';
ALTER TABLE indexer_instances ADD COLUMN expiry_notified_days INTEGER NOT NULL DEFAULT 0;

ALTER TABLE notifications ADD COLUMN on_expiry INTEGER NOT NULL DEFAULT 1;
