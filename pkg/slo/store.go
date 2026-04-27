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

package slo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What: a thin Postgres-backed CRUD over the slos and slo_state
// tables defined in 0005_slo.sql. Mirrors the shape of pkg/auth.Repo
// — one Store struct, one method per persistence concern.
//
// How: pgxpool.Pool is the only dependency. JSONB columns marshal
// through encoding/json on the way in and out. Tenant scoping is
// applied at the SQL level — every Get/List filters by tenant_id —
// so a misbehaving handler cannot accidentally read across tenants.
//
// Why pgxpool directly here, not behind another layer of interface:
// the surface is small (six methods) and every one is a single SQL
// statement. Wrapping an interface around it would only add boiler-
// plate. The evaluator depends on a narrow Reader interface (defined
// in evaluator.go) so unit tests can swap it for an in-memory fake.

// Store is the Postgres-backed SLO catalog + state store.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps an open pgxpool.Pool. Pool lifecycle is the
// caller's responsibility.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateSLO inserts a new SLO row. Validates first; returns
// ErrInvalidSLO without touching the DB on bad input.
//
// id must be supplied by the caller (handler chooses the prefix and
// random suffix). created_at is server-set via the column DEFAULT.
func (s *Store) CreateSLO(ctx context.Context, in SLO) (SLO, error) {
	if err := Validate(in); err != nil {
		return SLO{}, fmt.Errorf("%w: %v", ErrInvalidSLO, err)
	}
	filter, err := json.Marshal(in.SLIFilter)
	if err != nil {
		return SLO{}, fmt.Errorf("slo: marshal sli_filter: %w", err)
	}
	var out SLO
	var rawFilter []byte
	err = s.pool.QueryRow(ctx, `
        INSERT INTO slos (id, tenant_id, name, description, sli_kind, sli_filter,
                          objective_pct, window_seconds,
                          fast_burn_threshold, slow_burn_threshold,
                          notifier_url, enabled)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
        RETURNING id, tenant_id, name, description, sli_kind, sli_filter,
                  objective_pct, window_seconds,
                  fast_burn_threshold, slow_burn_threshold,
                  notifier_url, enabled, created_at`,
		in.ID, in.TenantID, in.Name, in.Description, string(in.SLIKind), filter,
		in.ObjectivePct, in.WindowSeconds,
		in.FastBurnThreshold, in.SlowBurnThreshold,
		in.NotifierURL, in.Enabled,
	).Scan(&out.ID, &out.TenantID, &out.Name, &out.Description,
		(*string)(&out.SLIKind), &rawFilter,
		&out.ObjectivePct, &out.WindowSeconds,
		&out.FastBurnThreshold, &out.SlowBurnThreshold,
		&out.NotifierURL, &out.Enabled, &out.CreatedAt)
	if err != nil {
		return SLO{}, fmt.Errorf("slo: insert: %w", err)
	}
	if len(rawFilter) > 0 {
		_ = json.Unmarshal(rawFilter, &out.SLIFilter)
	}
	return out, nil
}

// GetSLO returns the SLO matching (tenantID, id). Returns
// ErrSLONotFound when no row matches.
func (s *Store) GetSLO(ctx context.Context, tenantID, id string) (SLO, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT id, tenant_id, name, description, sli_kind, sli_filter,
               objective_pct, window_seconds,
               fast_burn_threshold, slow_burn_threshold,
               notifier_url, enabled, created_at
        FROM slos
        WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)

	var out SLO
	var rawFilter []byte
	if err := row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Description,
		(*string)(&out.SLIKind), &rawFilter,
		&out.ObjectivePct, &out.WindowSeconds,
		&out.FastBurnThreshold, &out.SlowBurnThreshold,
		&out.NotifierURL, &out.Enabled, &out.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SLO{}, ErrSLONotFound
		}
		return SLO{}, fmt.Errorf("slo: select: %w", err)
	}
	if len(rawFilter) > 0 {
		_ = json.Unmarshal(rawFilter, &out.SLIFilter)
	}
	return out, nil
}

// ListSLOs returns every SLO in the tenant. Order is created_at
// descending so the most recent SLOs come first — operators
// typically want to see what they just defined.
func (s *Store) ListSLOs(ctx context.Context, tenantID string) ([]SLO, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, tenant_id, name, description, sli_kind, sli_filter,
               objective_pct, window_seconds,
               fast_burn_threshold, slow_burn_threshold,
               notifier_url, enabled, created_at
        FROM slos
        WHERE tenant_id = $1
        ORDER BY created_at DESC`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("slo: list: %w", err)
	}
	defer rows.Close()
	out := []SLO{}
	for rows.Next() {
		var s SLO
		var rawFilter []byte
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.Description,
			(*string)(&s.SLIKind), &rawFilter,
			&s.ObjectivePct, &s.WindowSeconds,
			&s.FastBurnThreshold, &s.SlowBurnThreshold,
			&s.NotifierURL, &s.Enabled, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("slo: scan: %w", err)
		}
		if len(rawFilter) > 0 {
			_ = json.Unmarshal(rawFilter, &s.SLIFilter)
		}
		out = append(out, s)
	}
	return out, nil
}

// ListEnabled returns all enabled SLOs across every tenant. The
// evaluator uses this once per tick.
func (s *Store) ListEnabled(ctx context.Context) ([]SLO, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, tenant_id, name, description, sli_kind, sli_filter,
               objective_pct, window_seconds,
               fast_burn_threshold, slow_burn_threshold,
               notifier_url, enabled, created_at
        FROM slos
        WHERE enabled = TRUE
        ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("slo: list enabled: %w", err)
	}
	defer rows.Close()
	out := []SLO{}
	for rows.Next() {
		var s SLO
		var rawFilter []byte
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Name, &s.Description,
			(*string)(&s.SLIKind), &rawFilter,
			&s.ObjectivePct, &s.WindowSeconds,
			&s.FastBurnThreshold, &s.SlowBurnThreshold,
			&s.NotifierURL, &s.Enabled, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("slo: scan: %w", err)
		}
		if len(rawFilter) > 0 {
			_ = json.Unmarshal(rawFilter, &s.SLIFilter)
		}
		out = append(out, s)
	}
	return out, nil
}

// DeleteSLO removes one SLO row. Returns ErrSLONotFound when no row
// matches; the slo_state row is removed by the FK ON DELETE CASCADE.
func (s *Store) DeleteSLO(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM slos WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	if err != nil {
		return fmt.Errorf("slo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSLONotFound
	}
	return nil
}

// UpsertState replaces (or inserts) the state row for an SLO. The
// evaluator calls this on every tick.
func (s *Store) UpsertState(ctx context.Context, st State) error {
	var alertedAt any
	if !st.LastAlertedAt.IsZero() {
		alertedAt = st.LastAlertedAt
	}
	_, err := s.pool.Exec(ctx, `
        INSERT INTO slo_state (slo_id, last_evaluated_at, last_status, last_burn_rate, last_alerted_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (slo_id) DO UPDATE SET
            last_evaluated_at = EXCLUDED.last_evaluated_at,
            last_status       = EXCLUDED.last_status,
            last_burn_rate    = EXCLUDED.last_burn_rate,
            last_alerted_at   = COALESCE(EXCLUDED.last_alerted_at, slo_state.last_alerted_at)
    `, st.SLOID, st.LastEvaluatedAt, string(st.LastStatus), st.LastBurnRate, alertedAt)
	if err != nil {
		return fmt.Errorf("slo: upsert state: %w", err)
	}
	return nil
}

// GetState returns the latest state for an SLO. Missing → returns
// a zero State with Status=StatusUnknown so the evaluator can treat
// the absence-of-state as the initial transition cleanly.
func (s *Store) GetState(ctx context.Context, sloID string) (State, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT slo_id, last_evaluated_at, last_status, last_burn_rate, last_alerted_at
        FROM slo_state WHERE slo_id = $1`, sloID)
	var (
		st          State
		evaluatedAt *time.Time
		alertedAt   *time.Time
		burnRate    *float64
	)
	if err := row.Scan(&st.SLOID, &evaluatedAt, (*string)(&st.LastStatus), &burnRate, &alertedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return State{SLOID: sloID, LastStatus: StatusUnknown}, nil
		}
		return State{}, fmt.Errorf("slo: get state: %w", err)
	}
	if evaluatedAt != nil {
		st.LastEvaluatedAt = *evaluatedAt
	}
	if alertedAt != nil {
		st.LastAlertedAt = *alertedAt
	}
	if burnRate != nil {
		st.LastBurnRate = *burnRate
	}
	return st, nil
}
