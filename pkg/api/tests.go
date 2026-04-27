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

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shankar0123/netsite/pkg/api/middleware"
	"github.com/shankar0123/netsite/pkg/auth"
)

// What: HTTP handlers for /v1/tests — list, create, get, delete.
//
// How: each handler is a closure over a *pgxpool.Pool. Tenant
// scoping: every query is filtered by the authenticated user's
// TenantID, sourced from the request context populated by
// middleware.Authenticate. Cross-tenant reads/writes are not
// possible from a tenant-scoped session.
//
// Why pgxpool directly here, not through a TestsService struct: at
// v0 scale the SQL is short and there is no business logic worth
// abstracting. When we need cross-cutting behaviour (per-tenant
// quotas, audit trails) the service layer materialises around the
// existing handlers; until then it would be over-abstraction.

// testRequest is the body of POST /v1/tests.
type testRequest struct {
	Kind       string         `json:"kind"`
	Target     string         `json:"target"`
	IntervalMS int64          `json:"interval_ms"`
	TimeoutMS  int64          `json:"timeout_ms"`
	Enabled    *bool          `json:"enabled,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
}

// testResponse is the JSON shape for individual tests.
type testResponse struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	Kind       string         `json:"kind"`
	Target     string         `json:"target"`
	IntervalMS int64          `json:"interval_ms"`
	TimeoutMS  int64          `json:"timeout_ms"`
	Enabled    bool           `json:"enabled"`
	Config     map[string]any `json:"config"`
	CreatedAt  time.Time      `json:"created_at"`
}

// listTestsHandler returns GET /v1/tests — list all tests in the
// caller's tenant. Requires `tests:read` (viewer+).
func listTestsHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows, err := pool.Query(r.Context(), `
            SELECT id, tenant_id, kind, target, interval_ms, timeout_ms, enabled, config, created_at
            FROM tests
            WHERE tenant_id = $1
            ORDER BY created_at DESC`,
			u.TenantID)
		if err != nil {
			http.Error(w, "list tests: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := []testResponse{}
		for rows.Next() {
			var t testResponse
			var raw []byte
			if err := rows.Scan(&t.ID, &t.TenantID, &t.Kind, &t.Target,
				&t.IntervalMS, &t.TimeoutMS, &t.Enabled, &raw, &t.CreatedAt); err != nil {
				http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &t.Config)
			}
			out = append(out, t)
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// createTestHandler returns POST /v1/tests. Requires `tests:write`
// (operator+).
func createTestHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req testRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if !validKind(req.Kind) {
			http.Error(w, "kind must be dns, http, or tls", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Target) == "" {
			http.Error(w, "target is required", http.StatusBadRequest)
			return
		}
		if req.IntervalMS <= 0 {
			req.IntervalMS = 30000
		}
		if req.TimeoutMS <= 0 {
			req.TimeoutMS = 5000
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		configJSON, err := json.Marshal(req.Config)
		if err != nil {
			http.Error(w, "encode config", http.StatusBadRequest)
			return
		}

		id, err := newPrefixedID("tst")
		if err != nil {
			http.Error(w, "id gen: "+err.Error(), http.StatusInternalServerError)
			return
		}
		var (
			out testResponse
			raw []byte
		)
		err = pool.QueryRow(r.Context(), `
            INSERT INTO tests (id, tenant_id, kind, target, interval_ms, timeout_ms, enabled, config)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
            RETURNING id, tenant_id, kind, target, interval_ms, timeout_ms, enabled, config, created_at`,
			id, u.TenantID, req.Kind, req.Target, req.IntervalMS, req.TimeoutMS, enabled, configJSON,
		).Scan(&out.ID, &out.TenantID, &out.Kind, &out.Target,
			&out.IntervalMS, &out.TimeoutMS, &out.Enabled, &raw, &out.CreatedAt)
		if err != nil {
			http.Error(w, "create test: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out.Config)
		}
		writeJSON(w, http.StatusCreated, out)
	})
}

// getTestHandler returns GET /v1/tests/{id}. Requires `tests:read`.
func getTestHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		t, err := loadTest(r.Context(), pool, u.TenantID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get test: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, t)
	})
}

// deleteTestHandler returns DELETE /v1/tests/{id}. Requires
// `tests:write` (operator+).
func deleteTestHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		tag, err := pool.Exec(r.Context(),
			`DELETE FROM tests WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, id)
		if err != nil {
			http.Error(w, "delete test: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// loadTest fetches a single test scoped to tenantID.
func loadTest(ctx context.Context, pool *pgxpool.Pool, tenantID, id string) (testResponse, error) {
	var (
		t   testResponse
		raw []byte
	)
	err := pool.QueryRow(ctx, `
        SELECT id, tenant_id, kind, target, interval_ms, timeout_ms, enabled, config, created_at
        FROM tests
        WHERE tenant_id = $1 AND id = $2`,
		tenantID, id,
	).Scan(&t.ID, &t.TenantID, &t.Kind, &t.Target,
		&t.IntervalMS, &t.TimeoutMS, &t.Enabled, &raw, &t.CreatedAt)
	if err != nil {
		return testResponse{}, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &t.Config)
	}
	return t, nil
}

// validKind reports whether s is one of the canonical canary kinds.
// The Postgres CHECK constraint on tests.kind enforces this server-
// side; we duplicate the check here to return a 400 with a clear
// message instead of a 500 wrapping the constraint violation.
func validKind(s string) bool {
	switch s {
	case "dns", "http", "tls":
		return true
	}
	return false
}

// newPrefixedID returns "<prefix>-<32 hex chars>". Mirrors the
// pkg/auth helper but kept private here to avoid a circular import.
func newPrefixedID(prefix string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf[:]), nil
}

// _ = auth keeps the package import live for future per-test
// authorization helpers. Removed via dead-code cleanup if not used
// in v0.0.7.
var _ = auth.RoleAdmin
