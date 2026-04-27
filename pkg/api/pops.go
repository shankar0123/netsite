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
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shankar0123/netsite/pkg/api/middleware"
)

// What: HTTP handlers for /v1/pops — list, create, get, delete.
// Mirrors pkg/api/tests.go.
//
// How: identical pattern to tests — tenant-scoped queries, RBAC
// gating at the route registration site, simple struct schemas. The
// `pops` table is operator-managed: each ns-pop binary's YAML config
// references a pop_id; the operator inserts a matching row here so
// the controlplane knows what tests to schedule on each POP.
//
// Why operator-pre-registers (vs. self-register): Phase 0 has no
// per-POP NATS auth, so a self-register endpoint would have to be
// either anonymously writable (security smell) or tied to a shared
// bootstrap token (extra moving part). Pre-registration via the
// admin-protected API is the simplest model and matches how
// Prometheus, Datadog Agents, and others treat POPs/sites in their
// Phase 0 product.

// popRequest is the body of POST /v1/pops.
type popRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Region      string `json:"region,omitempty"`
	HealthURL   string `json:"health_url,omitempty"`
}

// popResponse is the JSON shape returned for individual POPs.
type popResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Region      string    `json:"region"`
	HealthURL   string    `json:"health_url"`
	CreatedAt   time.Time `json:"created_at"`
}

// listPopsHandler returns GET /v1/pops. Requires `pops:read`.
func listPopsHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows, err := pool.Query(r.Context(), `
            SELECT id, tenant_id, name, description, region, health_url, created_at
            FROM pops
            WHERE tenant_id = $1
            ORDER BY id ASC`,
			u.TenantID)
		if err != nil {
			http.Error(w, "list pops: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := []popResponse{}
		for rows.Next() {
			var p popResponse
			if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Description,
				&p.Region, &p.HealthURL, &p.CreatedAt); err != nil {
				http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
				return
			}
			out = append(out, p)
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// createPopHandler returns POST /v1/pops. Requires `pops:write`
// (operator+).
//
// The id field is provided by the operator (not server-generated)
// because POP IDs are operationally meaningful: ns-pop binaries
// reference them in YAML configs, prometheus scrape targets use
// them, log aggregators query by them. Letting the server hand back
// a random ID would force operators to round-trip a config update
// after each POP creation.
func createPopHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req popRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(req.ID, "pop-") {
			http.Error(w, "id must start with pop-", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		var p popResponse
		err := pool.QueryRow(r.Context(), `
            INSERT INTO pops (id, tenant_id, name, description, region, health_url)
            VALUES ($1, $2, $3, $4, $5, $6)
            RETURNING id, tenant_id, name, description, region, health_url, created_at`,
			req.ID, u.TenantID, req.Name, req.Description, req.Region, req.HealthURL,
		).Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Region, &p.HealthURL, &p.CreatedAt)
		if err != nil {
			http.Error(w, "create pop: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	})
}

// getPopHandler returns GET /v1/pops/{id}. Requires `pops:read`.
func getPopHandler(pool *pgxpool.Pool) http.Handler {
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
		var p popResponse
		err := pool.QueryRow(r.Context(), `
            SELECT id, tenant_id, name, description, region, health_url, created_at
            FROM pops
            WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, id,
		).Scan(&p.ID, &p.TenantID, &p.Name, &p.Description, &p.Region, &p.HealthURL, &p.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get pop: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
}

// deletePopHandler returns DELETE /v1/pops/{id}. Requires
// `pops:write` (operator+).
func deletePopHandler(pool *pgxpool.Pool) http.Handler {
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
			`DELETE FROM pops WHERE tenant_id = $1 AND id = $2`,
			u.TenantID, id)
		if err != nil {
			http.Error(w, "delete pop: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
