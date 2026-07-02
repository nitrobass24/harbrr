-- 0005_indexer_protocol.sql — acquisition protocol per indexer instance.
--
-- Adds the torrent/usenet "protocol" primitive to the persisted instance row.
-- Every existing and torrent-only instance defaults to 'torrent'; the Newznab
-- native family (a later leaf) is the only producer of 'usenet'. NOT NULL with a
-- DEFAULT so the additive ALTER backfills existing rows and test-only inserts
-- that omit the column stay valid. This leaf is pure plumbing — no behavior
-- changes until the serializer reads it.
ALTER TABLE indexer_instances ADD COLUMN protocol TEXT NOT NULL DEFAULT 'torrent';
