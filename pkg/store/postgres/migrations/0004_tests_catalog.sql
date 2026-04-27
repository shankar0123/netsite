-- 0004_tests_catalog.sql — POPs + Tests catalog.
--
-- What: two tables that together describe what the synthetic
--       monitoring layer is doing.
--         pops:  per-POP identity + region/description metadata.
--         tests: per-Test catalog entry — what to check, where, how
--                often, and which protocol-specific config knobs.
--
-- How:  CREATE TABLE IF NOT EXISTS, FK to tenants, CHECK constraints
--       on the role-equivalent enums (kind), JSONB for the open-
--       ended protocol config bag. Indices for the two lookup
--       patterns the API uses: list-by-tenant and lookup-by-id.
--
-- Why one migration for both: the foreign key chain is straight-
--       forward (both reference tenants only) and the API surface
--       lands together in v0.0.6. Splitting would force a no-op
--       second migration just to stage the second table.
--
-- Why JSONB on tests.config rather than per-protocol columns: the
--       protocol-specific config space (HTTP method, expected status,
--       DNS record type, TLS InsecureSkipVerify, future fields)
--       grows over time. JSONB lets us add fields without a
--       migration each time. The trade-off is that operators cannot
--       trivially query "all HTTP tests with method=POST" — the
--       JSONB ?| operator can but it's not as clean as a typed
--       column. Acceptable; we add typed columns for the fields
--       operators report querying frequently.

CREATE TABLE IF NOT EXISTS pops (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    region       TEXT NOT NULL DEFAULT '',
    health_url   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS pops_tenant_idx
    ON pops (tenant_id);

CREATE TABLE IF NOT EXISTS tests (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    kind          TEXT NOT NULL CHECK (kind IN ('dns', 'http', 'tls')),
    target        TEXT NOT NULL,
    interval_ms   BIGINT NOT NULL DEFAULT 30000,
    timeout_ms    BIGINT NOT NULL DEFAULT 5000,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS tests_tenant_idx
    ON tests (tenant_id);

CREATE INDEX IF NOT EXISTS tests_tenant_enabled_idx
    ON tests (tenant_id) WHERE enabled = TRUE;
