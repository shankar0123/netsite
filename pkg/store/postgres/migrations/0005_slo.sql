-- 0005_slo.sql — SLO definitions and per-SLO evaluator state.
--
-- What:
--   slos       — operator-declared SLOs. Each row defines an SLI
--                (which canary results to count, what counts as
--                "good"), an objective percentage, a window length,
--                and the two burn-rate thresholds the multi-window
--                multi-burn-rate evaluator uses.
--   slo_state  — one row per SLO carrying the latest evaluator
--                output. Transitions in last_status drive the
--                webhook notifier.
--
-- How:
--   - All `IF NOT EXISTS` so the migration runner's idempotency
--     contract holds.
--   - sli_filter is JSONB so adding new SLI dimensions later (pop_id,
--     status_code range) doesn't need a schema change.
--   - objective_pct in (0, 1) — exact 0% or 100% objectives are
--     special-cased nonsense in burn-rate math (division by zero
--     and rate undefined respectively).
--   - last_burn_rate is NUMERIC(10,4) — four decimal places is more
--     than the alerting decision needs, but enough for operator-
--     facing dashboards to show meaningful values.
--
-- Why slo_state is a separate table (not columns on slos): the SLO
-- definition (what the operator wrote) and the SLO state (what the
-- evaluator observed) have very different update patterns. Mixing
-- them invites accidental "the operator changed the threshold and
-- now last_burn_rate also got rewritten" bugs.

CREATE TABLE IF NOT EXISTS slos (
    id                    TEXT PRIMARY KEY,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id),
    name                  TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    sli_kind              TEXT NOT NULL CHECK (sli_kind IN ('canary_success')),
    sli_filter            JSONB NOT NULL DEFAULT '{}'::jsonb,
    objective_pct         NUMERIC(7, 6) NOT NULL CHECK (objective_pct > 0 AND objective_pct < 1),
    window_seconds        BIGINT NOT NULL CHECK (window_seconds > 0),
    fast_burn_threshold   NUMERIC(8, 4) NOT NULL DEFAULT 14.4 CHECK (fast_burn_threshold > 0),
    slow_burn_threshold   NUMERIC(8, 4) NOT NULL DEFAULT 6.0 CHECK (slow_burn_threshold > 0),
    notifier_url          TEXT NOT NULL DEFAULT '',
    enabled               BOOLEAN NOT NULL DEFAULT TRUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS slos_tenant_idx ON slos (tenant_id);
CREATE INDEX IF NOT EXISTS slos_enabled_idx ON slos (tenant_id) WHERE enabled = TRUE;

CREATE TABLE IF NOT EXISTS slo_state (
    slo_id              TEXT PRIMARY KEY REFERENCES slos(id) ON DELETE CASCADE,
    last_evaluated_at   TIMESTAMPTZ,
    last_status         TEXT NOT NULL DEFAULT 'unknown' CHECK (last_status IN ('unknown', 'no_data', 'ok', 'slow_burn', 'fast_burn')),
    last_burn_rate      NUMERIC(10, 4),
    last_alerted_at     TIMESTAMPTZ
);
