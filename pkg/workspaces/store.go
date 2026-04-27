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

package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What: a thin Postgres-backed CRUD over the workspaces table from
// 0006_workspaces.sql. One method per persistence concern; every
// query is a single SQL statement.
//
// How: pgxpool.Pool is the only dependency. The Views slice is
// JSONB-serialised on the way in and JSON-deserialised on the way
// out. Tenant scoping is applied at the SQL layer — every Get/List
// filters by tenant_id — so a misbehaving handler cannot
// accidentally read across tenants.
//
// Why pgxpool directly here, not behind an interface: same logic as
// pkg/auth and pkg/slo — the surface is small, every method is one
// SQL statement, and the service layer above already has a Reader
// interface for tests to mock.

// Store is the Postgres-backed workspaces persistence layer.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps an open pgxpool.Pool. Lifetime is the caller's
// responsibility.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Insert creates a new workspace row. id, tenantID, ownerUserID
// must all be supplied by the caller; created_at / updated_at are
// server-set via the column DEFAULTs.
func (s *Store) Insert(ctx context.Context, w Workspace) (Workspace, error) {
	views, err := json.Marshal(w.Views)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspaces: marshal views: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
        INSERT INTO workspaces (id, tenant_id, owner_user_id, name, description, views)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, tenant_id, owner_user_id, name, description, views,
                  share_slug, share_expires_at, created_at, updated_at`,
		w.ID, w.TenantID, w.OwnerUserID, w.Name, w.Description, views)
	return scanRow(row)
}

// Get returns the workspace matching (tenantID, id). Returns
// ErrWorkspaceNotFound on no row.
func (s *Store) Get(ctx context.Context, tenantID, id string) (Workspace, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT id, tenant_id, owner_user_id, name, description, views,
               share_slug, share_expires_at, created_at, updated_at
        FROM workspaces
        WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	w, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, err
}

// List returns every workspace owned by the tenant, ordered by
// updated_at descending so the most recently touched workspace
// surfaces first.
func (s *Store) List(ctx context.Context, tenantID string) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, tenant_id, owner_user_id, name, description, views,
               share_slug, share_expires_at, created_at, updated_at
        FROM workspaces
        WHERE tenant_id = $1
        ORDER BY updated_at DESC`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("workspaces: list: %w", err)
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// Update applies non-nil fields from req. Returns ErrWorkspaceNot-
// Found when no row matches (tenantID, id).
//
// Why a single multi-column UPDATE rather than per-field: the
// JSONB and TEXT cases compose cleanly into one statement using
// COALESCE on placeholders that the caller can pass either as the
// new value or NULL. This avoids the "build the SQL string
// dynamically" trap we'd otherwise need.
func (s *Store) Update(ctx context.Context, tenantID, id string, req UpdateRequest) (Workspace, error) {
	var nameArg, descArg any
	if req.Name != nil {
		nameArg = *req.Name
	}
	if req.Description != nil {
		descArg = *req.Description
	}
	var viewsArg any
	if req.Views != nil {
		bs, err := json.Marshal(*req.Views)
		if err != nil {
			return Workspace{}, fmt.Errorf("workspaces: marshal views: %w", err)
		}
		viewsArg = bs
	}
	row := s.pool.QueryRow(ctx, `
        UPDATE workspaces SET
            name        = COALESCE($3, name),
            description = COALESCE($4, description),
            views       = COALESCE($5::jsonb, views),
            updated_at  = now()
        WHERE tenant_id = $1 AND id = $2
        RETURNING id, tenant_id, owner_user_id, name, description, views,
                  share_slug, share_expires_at, created_at, updated_at`,
		tenantID, id, nameArg, descArg, viewsArg)
	w, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, err
}

// Delete removes one workspace. Returns ErrWorkspaceNotFound when
// no row matches.
func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM workspaces WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	if err != nil {
		return fmt.Errorf("workspaces: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkspaceNotFound
	}
	return nil
}

// SetShare writes a share_slug + share_expires_at pair. Pass empty
// slug + nil expiresAt to clear the share. The slug column is
// UNIQUE; collisions surface as a pgx error which the service maps
// to a retry.
func (s *Store) SetShare(ctx context.Context, tenantID, id string, slug string, expiresAt *time.Time) (Workspace, error) {
	var slugArg any
	if slug != "" {
		slugArg = slug
	}
	row := s.pool.QueryRow(ctx, `
        UPDATE workspaces SET
            share_slug       = $3,
            share_expires_at = $4,
            updated_at       = now()
        WHERE tenant_id = $1 AND id = $2
        RETURNING id, tenant_id, owner_user_id, name, description, views,
                  share_slug, share_expires_at, created_at, updated_at`,
		tenantID, id, slugArg, expiresAt)
	w, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, err
}

// GetByShareSlug resolves a public slug to its workspace. Returns
// ErrShareNotFound when the slug is unknown or has expired. The
// expiry check is done in SQL so an expired slug never returns its
// content even if the row still exists.
func (s *Store) GetByShareSlug(ctx context.Context, slug string, now time.Time) (Workspace, error) {
	if slug == "" {
		return Workspace{}, ErrShareNotFound
	}
	row := s.pool.QueryRow(ctx, `
        SELECT id, tenant_id, owner_user_id, name, description, views,
               share_slug, share_expires_at, created_at, updated_at
        FROM workspaces
        WHERE share_slug = $1
          AND (share_expires_at IS NULL OR share_expires_at > $2)`,
		slug, now)
	w, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrShareNotFound
	}
	return w, err
}

// scanRow centralises the one-row destructuring + JSONB unmarshal.
// Both QueryRow callers and the row iterator inside List route
// through here so a future schema change rewrites in one place.
//
// The interface argument lets us accept both pgx.Row (for
// QueryRow) and pgx.Rows (for Query iteration).
func scanRow(row interface {
	Scan(...any) error
}) (Workspace, error) {
	var (
		w        Workspace
		viewsRaw []byte
		slug     *string
		expires  *time.Time
	)
	if err := row.Scan(
		&w.ID, &w.TenantID, &w.OwnerUserID, &w.Name, &w.Description, &viewsRaw,
		&slug, &expires, &w.CreatedAt, &w.UpdatedAt,
	); err != nil {
		return Workspace{}, err
	}
	if len(viewsRaw) > 0 {
		_ = json.Unmarshal(viewsRaw, &w.Views)
	}
	if w.Views == nil {
		w.Views = []View{}
	}
	if slug != nil {
		w.ShareSlug = *slug
	}
	w.ShareExpiresAt = expires
	return w, nil
}
