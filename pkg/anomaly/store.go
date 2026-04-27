// Copyright 2026 Shankar Reddy. All Rights Reserved.
//
// Licensed under the Business Source License 1.1 (the "License").
// You may not use this file except in compliance with the License.
// A copy of the License is bundled with this distribution at ./LICENSE
// in the repository root, or available at https://mariadb.com/bsl11/.
//
// Licensed Work:  NetSite
// Change Date:    2125-01-01
// Change License: Apache License, Version 2.0
//
// On the Change Date, the rights granted in this License terminate and
// you are granted rights under the Change License instead.

package anomaly

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What: a thin Postgres-backed CRUD over the `anomaly_state` table
// from 0008_anomaly_state.sql, plus the read-side `tests` enumerator
// the evaluator needs to know which (tenant, test) pairs to score.
//
// How: pgxpool.Pool only. Two persistence concerns lumped into one
// store because they're always used together (the evaluator lists
// tests then upserts state per test). Splitting would force the
// evaluator to take two store dependencies in its constructor for no
// gain.
//
// Why pgxpool directly here, not behind another layer of interface:
// the surface is small (four methods) and every one is a single SQL
// statement. The evaluator depends on a narrow Reader/Writer pair
// (defined in evaluator.go) so unit tests can swap in an in-memory
// fake without dragging in a Postgres testcontainer.

// TestRef is the minimum the evaluator needs to know about a test:
// its identity (tenant + test_id) so it can fetch a series and
// upsert state. Loaded by ListEnabledTests.
type TestRef struct {
	TenantID string
	TestID   string
}

// VerdictRow is the persisted shape of a Verdict — the evaluator
// converts the in-memory Verdict to a row and back.
type VerdictRow struct {
	TenantID    string
	TestID      string
	Metric      string
	Method      Method
	Severity    Severity
	Suppressed  bool
	LastValue   float64
	Forecast    float64
	Residual    float64
	MAD         float64
	MADUnits    float64
	Reason      string
	LastPointAt time.Time
	EvaluatedAt time.Time
}

// Store is the Postgres-backed anomaly state store + tests enumerator.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps an open pgxpool.Pool. Pool lifecycle is the
// caller's responsibility (mirrors pkg/slo, pkg/workspaces).
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ListEnabledTests returns one TestRef per enabled row in the tests
// catalog across every tenant. The evaluator iterates this list per
// tick. Disabled tests are skipped — paused canaries should stay
// paused for the anomaly detector too.
//
// We list across every tenant rather than scoping by tenant because
// the evaluator is a single goroutine running for the whole control
// plane, not a per-tenant fan-out. Phase 5 (RBAC for multi-tenant
// hosting) revisits if we need per-tenant evaluator schedules.
func (s *Store) ListEnabledTests(ctx context.Context) ([]TestRef, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT tenant_id, id
        FROM tests
        WHERE enabled = TRUE
        ORDER BY tenant_id, id`)
	if err != nil {
		return nil, fmt.Errorf("anomaly: list enabled tests: %w", err)
	}
	defer rows.Close()
	out := make([]TestRef, 0, 32)
	for rows.Next() {
		var r TestRef
		if err := rows.Scan(&r.TenantID, &r.TestID); err != nil {
			return nil, fmt.Errorf("anomaly: scan test ref: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anomaly: rows err: %w", err)
	}
	return out, nil
}

// UpsertVerdict replaces the (tenant_id, test_id, metric) row with
// the supplied Verdict. Idempotent: re-running on the same
// (tenant, test, metric) overwrites the row in place.
func (s *Store) UpsertVerdict(ctx context.Context, row VerdictRow) error {
	if row.TenantID == "" || row.TestID == "" || row.Metric == "" {
		return errors.New("anomaly: UpsertVerdict requires tenant_id, test_id, metric")
	}
	_, err := s.pool.Exec(ctx, `
        INSERT INTO anomaly_state (
            tenant_id, test_id, metric,
            method, severity, suppressed,
            last_value, forecast, residual, mad, mad_units,
            reason, last_point_at, evaluated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
        ON CONFLICT (tenant_id, test_id, metric) DO UPDATE SET
            method        = EXCLUDED.method,
            severity      = EXCLUDED.severity,
            suppressed    = EXCLUDED.suppressed,
            last_value    = EXCLUDED.last_value,
            forecast      = EXCLUDED.forecast,
            residual      = EXCLUDED.residual,
            mad           = EXCLUDED.mad,
            mad_units     = EXCLUDED.mad_units,
            reason        = EXCLUDED.reason,
            last_point_at = EXCLUDED.last_point_at,
            evaluated_at  = EXCLUDED.evaluated_at`,
		row.TenantID, row.TestID, row.Metric,
		string(row.Method), string(row.Severity), row.Suppressed,
		row.LastValue, row.Forecast, row.Residual, row.MAD, row.MADUnits,
		row.Reason, row.LastPointAt, row.EvaluatedAt,
	)
	if err != nil {
		return fmt.Errorf("anomaly: upsert verdict: %w", err)
	}
	return nil
}

// ListVerdicts returns every cached verdict for a tenant. Ordering
// is stable: (test_id, metric) ascending so a UI can render a sorted
// list deterministically.
func (s *Store) ListVerdicts(ctx context.Context, tenantID string) ([]VerdictRow, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT tenant_id, test_id, metric,
               method, severity, suppressed,
               last_value, forecast, residual, mad, mad_units,
               reason, last_point_at, evaluated_at
        FROM anomaly_state
        WHERE tenant_id = $1
        ORDER BY test_id, metric`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("anomaly: list verdicts: %w", err)
	}
	defer rows.Close()
	out := make([]VerdictRow, 0, 16)
	for rows.Next() {
		v, err := scanVerdictRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anomaly: rows err: %w", err)
	}
	return out, nil
}

// GetVerdict returns the latest verdict for a (tenant, test, metric)
// triple. Returns ErrVerdictNotFound when the row does not exist —
// callers translate to 404.
func (s *Store) GetVerdict(ctx context.Context, tenantID, testID, metric string) (VerdictRow, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT tenant_id, test_id, metric,
               method, severity, suppressed,
               last_value, forecast, residual, mad, mad_units,
               reason, last_point_at, evaluated_at
        FROM anomaly_state
        WHERE tenant_id = $1 AND test_id = $2 AND metric = $3`,
		tenantID, testID, metric)
	v, err := scanVerdictRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VerdictRow{}, ErrVerdictNotFound
		}
		return VerdictRow{}, err
	}
	return v, nil
}

// scanner is the narrow Scan-able interface satisfied by both
// pgx.Row and pgx.Rows so scanVerdictRow can serve both code paths
// (Get vs List) without duplication.
type scanner interface {
	Scan(dest ...any) error
}

func scanVerdictRow(s scanner) (VerdictRow, error) {
	var v VerdictRow
	var method, severity string
	if err := s.Scan(
		&v.TenantID, &v.TestID, &v.Metric,
		&method, &severity, &v.Suppressed,
		&v.LastValue, &v.Forecast, &v.Residual, &v.MAD, &v.MADUnits,
		&v.Reason, &v.LastPointAt, &v.EvaluatedAt,
	); err != nil {
		return VerdictRow{}, err
	}
	v.Method = Method(method)
	v.Severity = Severity(severity)
	return v, nil
}

// ErrVerdictNotFound is returned by GetVerdict when no row matches.
// Callers translate to 404.
var ErrVerdictNotFound = errors.New("anomaly: verdict not found")
