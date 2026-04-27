-- 0001_init.sql — placeholder validating the migration runner round-trip.
--
-- What: creates a small sentinel table and inserts a single row.
-- How:  CREATE TABLE IF NOT EXISTS + INSERT ... ON CONFLICT DO NOTHING
--       make this trivially idempotent at the SQL level, independent of
--       the runner's tracking table.
-- Why:  the runner's idempotency assertion depends on TWO things being
--       true: (1) the runner skips already-applied files, and (2) even
--       if the tracking row is missing, re-running the SQL is a no-op.
--       This file exercises (2). Real schema migrations begin in Task
--       0.10 (auth_core: tenants, users, sessions).

CREATE TABLE IF NOT EXISTS _migrations_smoke (
    sentinel    TEXT PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO _migrations_smoke(sentinel) VALUES ('0001_init')
ON CONFLICT (sentinel) DO NOTHING;
