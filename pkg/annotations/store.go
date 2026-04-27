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

package annotations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What: pgxpool-backed CRUD for the annotations table from
// 0007_annotations.sql. Four methods cover the full life cycle —
// Insert, Get, List, Delete. There is no Update; annotations are
// immutable by design.
//
// How: each method is one SQL statement. Tenant scoping is at the
// SQL layer on every read; the List query uses the composite index
// (tenant_id, scope, scope_id, at) so the per-canary timeline read
// is always a tight range scan.
//
// Why no Update: the audit trail is the point. Operators who write
// the wrong note delete it and write a new one; the deletion is
// itself a fact in the timeline.

// Store is the Postgres-backed annotations persistence layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps an open pgxpool.Pool. Lifetime is the caller's.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Insert writes a new annotation row. id, tenantID, authorID, scope
// must all be supplied by the caller (the Service mints id +
// stamps timestamps).
func (s *Store) Insert(ctx context.Context, a Annotation) (Annotation, error) {
	row := s.pool.QueryRow(ctx, `
        INSERT INTO annotations (id, tenant_id, scope, scope_id, at, body_md, author_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING id, tenant_id, scope, scope_id, at, body_md, author_id, created_at`,
		a.ID, a.TenantID, string(a.Scope), a.ScopeID, a.At, a.BodyMD, a.AuthorID)
	return scanRow(row)
}

// Get returns the annotation matching (tenantID, id). Returns
// ErrAnnotationNotFound when no row matches.
func (s *Store) Get(ctx context.Context, tenantID, id string) (Annotation, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT id, tenant_id, scope, scope_id, at, body_md, author_id, created_at
        FROM annotations
        WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	a, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Annotation{}, ErrAnnotationNotFound
	}
	return a, err
}

// List filters annotations for a tenant. Empty filter fields match
// any value. Order is `at ASC` so consumers reading a canary's
// timeline see events in chronological order; flip to DESC at the
// caller if a tail-first view is needed.
//
// Why we build the SQL piecewise rather than passing a single
// pre-baked query: the optional WHERE clauses (scope, scope_id,
// from, to) compose multiplicatively. Building the predicate list
// dynamically keeps the resulting SQL minimal and lets the planner
// pick the best path against the composite index.
func (s *Store) List(ctx context.Context, tenantID string, f ListFilter) ([]Annotation, error) {
	var (
		preds = []string{"tenant_id = $1"}
		args  = []any{tenantID}
	)
	if f.Scope != "" {
		preds = append(preds, fmt.Sprintf("scope = $%d", len(args)+1))
		args = append(args, string(f.Scope))
	}
	if f.ScopeID != "" {
		preds = append(preds, fmt.Sprintf("scope_id = $%d", len(args)+1))
		args = append(args, f.ScopeID)
	}
	if f.From != nil {
		preds = append(preds, fmt.Sprintf("at >= $%d", len(args)+1))
		args = append(args, *f.From)
	}
	if f.To != nil {
		preds = append(preds, fmt.Sprintf("at < $%d", len(args)+1))
		args = append(args, *f.To)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	q := `SELECT id, tenant_id, scope, scope_id, at, body_md, author_id, created_at
          FROM annotations
          WHERE ` + strings.Join(preds, " AND ") +
		fmt.Sprintf(" ORDER BY at ASC LIMIT %d", limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("annotations: list: %w", err)
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		a, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Delete removes one annotation. Returns ErrAnnotationNotFound when
// no row matches.
func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM annotations WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	if err != nil {
		return fmt.Errorf("annotations: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAnnotationNotFound
	}
	return nil
}

// scanRow centralises the one-row destructuring. The interface
// argument lets us accept both pgx.Row (from QueryRow) and pgx.Rows
// (from Query iteration).
func scanRow(row interface {
	Scan(...any) error
}) (Annotation, error) {
	var (
		a     Annotation
		scope string
		at    time.Time
	)
	if err := row.Scan(
		&a.ID, &a.TenantID, &scope, &a.ScopeID, &at, &a.BodyMD, &a.AuthorID, &a.CreatedAt,
	); err != nil {
		return Annotation{}, err
	}
	a.Scope = Scope(scope)
	a.At = at
	return a, nil
}

// ensure *Store satisfies Reader and Mutator at compile time.
var (
	_ Reader  = (*Store)(nil)
	_ Mutator = (*Store)(nil)
)
