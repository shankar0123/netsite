-- 0001_init.sql — placeholder validating the schema applier round-trip.
--
-- What: creates a tiny sentinel table.
-- How:  CREATE TABLE IF NOT EXISTS makes this idempotent at the SQL
--       layer, independent of the applier's tracking table.
-- Why:  the applier's idempotency claim depends on TWO things being
--       true: (1) the runner skips already-applied files, and (2) even
--       if the tracking row is missing, re-running the SQL is a no-op.
--       This file exercises (2). Real schemas (canary_results, BGP
--       updates, flow records, PCAP fingerprints) arrive in their own
--       Phase 0 and Phase 1 tasks.

CREATE TABLE IF NOT EXISTS _ch_schema_smoke (
    sentinel   String,
    created_at DateTime64(3, 'UTC') DEFAULT now64()
) ENGINE = ReplacingMergeTree(created_at)
ORDER BY sentinel
