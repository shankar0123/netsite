-- Copyright 2026 Shankar Reddy. All Rights Reserved.
--
-- Licensed under the Business Source License 1.1 (the "License").
-- You may not use this file except in compliance with the License.
-- A copy of the License is bundled with this distribution at ./LICENSE
-- in the repository root, or available at https://mariadb.com/bsl11/.
--
-- Licensed Work:  NetSite
-- Change Date:    2125-01-01
-- Change License: Apache License, Version 2.0
--
-- On the Change Date, the rights granted in this License terminate and
-- you are granted rights under the Change License instead.
--
-- 0008_anomaly_state.sql — anomaly detector verdict cache.
--
-- What:
--   One row per (tenant_id, test_id, metric) recording the latest
--   anomaly detector Verdict. The evaluator goroutine refreshes
--   these rows on each tick; the REST surface (/v1/anomaly/*) reads
--   them. A "row exists" answer means "we have evaluated this
--   recently"; absence means "either the detector hasn't run yet or
--   the test was created after the last tick".
--
-- How:
--   - Composite PK is (tenant_id, test_id, metric) so a tenant
--     gets at most one row per (test, metric). The evaluator
--     UPSERTs on every tick, replacing the row so an operator
--     reading at any moment gets the latest verdict (not history).
--   - history is intentionally NOT stored here. The verdict reason
--     and the residual carry the audit trail; longitudinal anomaly
--     trend analysis goes through the underlying canary_results
--     ClickHouse data, not this Postgres state. Phase 1 may add a
--     second table `anomaly_events` keyed by event_id for the
--     subset that crosses SeverityAnomaly+, mirroring slo_state /
--     slo_events.
--   - severity is constrained via CHECK to the four canonical
--     values (none / watch / anomaly / critical). The catch-all
--     'insufficient_data' case is encoded as method, not severity
--     (we always set severity='none' there).
--   - method is constrained via CHECK to the three canonical
--     values (holt_winters / seasonal_decompose / insufficient_data).
--   - reason is free-text human prose from the detector. Bounded
--     informally by the detector itself.
--   - last_value / forecast / residual / mad / mad_units mirror the
--     Verdict struct. residual is signed (NetSite's metrics are
--     all non-negative, but we want sign on the residual so an
--     operator can see "value below forecast" vs "above forecast").
--   - evaluated_at is server-set by the evaluator at the moment of
--     evaluation (after Detect returns). NOT a column DEFAULT; the
--     evaluator owns the timestamp so an operator looking at "last
--     evaluated 5m ago" can rely on it being the actual detect call.
--
-- Why a single composite index covers reads:
--   The two read patterns are "give me one verdict for (tenant,
--   test, metric)" — covered by the PK — and "give me all verdicts
--   for tenant X" — covered by the leading-tenant_id portion of the
--   PK. No additional indexes needed.
--
-- Why no FK to tests(id):
--   Tests can be deleted; we do not want a deleted test to silently
--   prune its last verdict before the operator reads it (the verdict
--   may explain WHY they deleted the test). The evaluator naturally
--   stops refreshing rows for deleted tests, so the rows age out as
--   "stale verdicts". Phase 1 adds an explicit GC sweep with a
--   configurable retention window (default 30 days).

CREATE TABLE IF NOT EXISTS anomaly_state (
    tenant_id      TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    test_id        TEXT NOT NULL,
    metric         TEXT NOT NULL,
    method         TEXT NOT NULL CHECK (method IN ('holt_winters', 'seasonal_decompose', 'insufficient_data')),
    severity       TEXT NOT NULL CHECK (severity IN ('none', 'watch', 'anomaly', 'critical')),
    suppressed     BOOLEAN NOT NULL DEFAULT FALSE,
    last_value     DOUBLE PRECISION NOT NULL,
    forecast       DOUBLE PRECISION NOT NULL,
    residual       DOUBLE PRECISION NOT NULL,
    mad            DOUBLE PRECISION NOT NULL,
    mad_units      DOUBLE PRECISION NOT NULL,
    reason         TEXT NOT NULL DEFAULT '',
    last_point_at  TIMESTAMPTZ NOT NULL,
    evaluated_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, test_id, metric)
);
