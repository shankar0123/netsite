# ClickHouse schema files

> The schema applier lives in [`../schema.go`](../schema.go). The integration
> test that proves idempotency lives in [`../schema_test.go`](../schema_test.go).

## Rules

1. **Forward-only.** No down migrations. To revert, write a new file
   that undoes the change. Same rationale as the Postgres
   `migrations/`: down migrations encourage environment drift.
2. **Idempotent.** Every file's SQL must be runnable twice as a no-op.
   In ClickHouse this means `CREATE TABLE IF NOT EXISTS`,
   `CREATE MATERIALIZED VIEW IF NOT EXISTS`, `CREATE DATABASE IF NOT
   EXISTS`, and `ALTER TABLE … ADD COLUMN IF NOT EXISTS`. ClickHouse
   does not support transactional DDL; idempotency at the SQL layer is
   defense-in-depth in case the tracking table is wiped.
3. **One statement per file.** ClickHouse's native protocol does not
   accept multi-statement payloads in a single Exec. Splitting one
   logical change across two files (e.g. `0007_create_users.sql` then
   `0008_users_index.sql`) is fine; bundling unrelated DDL into one
   file is not.
4. **Never edit a file once it has been applied to any environment.**
   Editing recorded SQL makes the schema's history unverifiable.
5. **Naming: `NNNN_short_description.sql`.** Four-digit zero-padded
   prefix; the applier sorts lex-order, which matches numeric for
   zero-padded prefixes.

## Engine, partition key, order key — convention

Every table choice has three load-bearing decisions. Document them in a
comment block at the top of each file:

```sql
-- Engine:        MergeTree
-- Partition key: toYYYYMM(observed_at)
-- Order key:     (tenant_id, test_id, observed_at)
-- TTL:           observed_at + INTERVAL 90 DAY
-- Why:           query patterns are tenant-scoped time-window scans;
--                partitioning by month keeps drops cheap; order key
--                clusters rows for the most common WHERE filters.
```

Engine choice cheat-sheet:

| Workload | Engine |
|---|---|
| Append-only events with high write rate | `MergeTree` |
| Append-only events that need dedup on a key | `ReplacingMergeTree(version)` |
| Append-only events that need pre-aggregation | `AggregatingMergeTree` or materialized view fed by `MergeTree` |
| Slowly changing dimension table | `ReplacingMergeTree` with periodic OPTIMIZE … FINAL |
| Internal metadata (tracking, catalogs, small ref tables) | `ReplacingMergeTree` |

Order-key rule of thumb: list columns in **decreasing cardinality**
order, with the most selective leading column first when query patterns
allow. Time columns go last, not first. This keeps the granule index
small and skip-scans cheap.

## Tracking table

`_ch_schema_applied` records which files have been applied. Inspect with
`FINAL` so ReplacingMergeTree dedupes in-flight merges:

```sql
SELECT name, applied_at
FROM _ch_schema_applied FINAL
ORDER BY name;
```

## Adding a schema file

1. Pick the next prefix.
2. Create `NNNN_<description>.sql`. Begin with a comment block per the
   project doc standard (CLAUDE.md → Documentation Discipline): what
   the file does, the engine/partition/order decisions, why now.
3. One statement only.
4. Update or add the integration-test assertion in `../schema_test.go`
   that proves the new file is idempotent and produces the expected
   end state.
5. Run `make test-integration` locally (requires Docker for
   testcontainers).

## Why no `clickhouse-migrator` / `dbmate` / `goose`

Same answer as `pkg/store/postgres/migrations/README.md`: a 200-line
runner with no extra dependencies covers every case NetSite needs
(forward-only, idempotent, embedded SQL, lex-ordered application).
External tools introduce a CLI, a config format, and a dependency tree
without delivering anything we use.
