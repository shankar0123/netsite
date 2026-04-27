-- 0002_canary_results.sql — first real ClickHouse table.
--
-- What:  the time-series store for synthetic canary results — every
--        ICMP, DNS, HTTP, and TLS probe a POP runs lands here.
-- How:   one CREATE TABLE statement using the MergeTree engine with
--        per-month partitioning, a tenant-aware order key, and a 90-
--        day TTL on raw rows.
-- Why this engine, partition, and order key:
--
--   Engine = MergeTree
--     The append-only-events workload writes once and reads in
--     time-bounded scans. MergeTree is the canonical choice. We
--     considered ReplacingMergeTree (would dedupe on a key) — not
--     needed: each canary observation is genuinely unique by
--     (tenant_id, test_id, observed_at, pop_id) and the at-least-once
--     guarantee from NATS is enforced at insert time (publisher uses
--     deduplication headers in Phase 1).
--
--   Partition = toYYYYMM(observed_at)
--     One partition per month keeps DROP PARTITION (used by TTL) cheap
--     and bounds the file count Linux ever has to inotify on a single
--     directory. Per-day partitioning was considered and rejected:
--     thousand-tests-per-POP × dozens-of-POPs over 90 days produces
--     ~3000 partitions, well below ClickHouse's recommended ceiling
--     but also above the threshold where the partition scan itself
--     starts to dominate query planning.
--
--   Order key = (tenant_id, test_id, observed_at)
--     The canonical query pattern is "show me latency_p95 for
--     tenant=X, test=Y, over the last 24h." Putting tenant_id first
--     means the granule index can skip every granule that does not
--     belong to the queried tenant — the most aggressive prune ClickHouse
--     can do. test_id second is the next selective filter. observed_at
--     last clusters rows for the time-window scan.
--     We deliberately do NOT lead with observed_at: ClickHouse's index
--     is a "skip index" not a B-tree, and leading with low-cardinality
--     time would make every tenant's data physically interleaved.
--
--   TTL = observed_at + INTERVAL 90 DAY
--     Raw rows expire after 90 days. Aggregated rollups (p50/p95/p99
--     per minute) land in a materialized view fed by this table —
--     that view's retention is configured separately in Phase 1.
--     Tenants that need longer raw retention override the TTL via a
--     per-tenant materialized view, not by changing this base table.
--
--   LowCardinality(String) on error_kind
--     error_kind has at most ~30 distinct values across the project's
--     lifetime ("dns_timeout", "tls_handshake_failed", "connect_refused",
--     etc.). LowCardinality stores them as dictionary-encoded UInt8,
--     which compresses to ~1 byte per row vs ~16+ for raw String.
--     Per the ClickHouse docs, LowCardinality pays off whenever the
--     cardinality is below ~10k and the column is read in queries;
--     error_kind is read in nearly every aggregate.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS makes a re-apply a no-op.

CREATE TABLE IF NOT EXISTS canary_results (
    tenant_id     String,
    test_id       String,
    pop_id        String,
    observed_at   DateTime64(3, 'UTC'),
    latency_ms    Float32,
    dns_ms        Float32,
    connect_ms    Float32,
    tls_ms        Float32,
    ttfb_ms       Float32,
    status_code   UInt16,
    error_kind    LowCardinality(String),
    ja3           String,
    ja4           String,
    _inserted_at  DateTime DEFAULT now()
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (tenant_id, test_id, observed_at)
TTL toDateTime(observed_at) + INTERVAL 90 DAY
SETTINGS index_granularity = 8192
