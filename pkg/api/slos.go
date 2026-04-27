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
	"time"

	"github.com/shankar0123/netsite/pkg/api/middleware"
	"github.com/shankar0123/netsite/pkg/slo"
)

// What: HTTP handlers for /v1/slos. Same pattern as pkg/api/tests.go
// — closure over slo.Store, tenant scoping from context, RBAC at the
// route registration site.
//
// Why a separate slo.Store rather than calling pkg/api code directly:
// SLO definitions cross the package boundary. Threading a Store
// dependency through Config keeps the test surface narrow (handlers
// can be unit-tested with an in-memory Store fake) and the
// controlplane main wires the concrete one.

// sloRequest is the body of POST /v1/slos.
type sloRequest struct {
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	SLIKind           string         `json:"sli_kind"`
	SLIFilter         map[string]any `json:"sli_filter,omitempty"`
	ObjectivePct      float64        `json:"objective_pct"`
	WindowSeconds     int64          `json:"window_seconds"`
	FastBurnThreshold float64        `json:"fast_burn_threshold,omitempty"`
	SlowBurnThreshold float64        `json:"slow_burn_threshold,omitempty"`
	NotifierURL       string         `json:"notifier_url,omitempty"`
	Enabled           *bool          `json:"enabled,omitempty"`
}

// sloResponse is what /v1/slos returns. Mirrors slo.SLO + an
// optional `state` block populated from the slo_state table when
// the evaluator has ticked at least once.
//
// State is nullable on the wire: an SLO that has never been
// evaluated returns `"state": null`, and the React client renders
// that as "pending first evaluation". An SLO with state.last_status
// = "ok" is a different (better) operational answer than "we have
// no idea yet" — the wire format must distinguish them.
type sloResponse struct {
	ID                string         `json:"id"`
	TenantID          string         `json:"tenant_id"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	SLIKind           string         `json:"sli_kind"`
	SLIFilter         map[string]any `json:"sli_filter"`
	ObjectivePct      float64        `json:"objective_pct"`
	WindowSeconds     int64          `json:"window_seconds"`
	FastBurnThreshold float64        `json:"fast_burn_threshold"`
	SlowBurnThreshold float64        `json:"slow_burn_threshold"`
	NotifierURL       string         `json:"notifier_url"`
	Enabled           bool           `json:"enabled"`
	CreatedAt         time.Time      `json:"created_at"`
	State             *sloStateBlock `json:"state"`
}

// sloStateBlock is the JSON-rendered slo_state row. Nil when the
// SLO has never been evaluated.
type sloStateBlock struct {
	LastEvaluatedAt time.Time  `json:"last_evaluated_at"`
	LastStatus      string     `json:"last_status"`
	LastBurnRate    float64    `json:"last_burn_rate"`
	LastAlertedAt   *time.Time `json:"last_alerted_at"`
}

func toSLOResponse(s slo.SLO) sloResponse {
	return sloResponse{
		ID: s.ID, TenantID: s.TenantID, Name: s.Name, Description: s.Description,
		SLIKind: string(s.SLIKind), SLIFilter: s.SLIFilter,
		ObjectivePct: s.ObjectivePct, WindowSeconds: s.WindowSeconds,
		FastBurnThreshold: s.FastBurnThreshold, SlowBurnThreshold: s.SlowBurnThreshold,
		NotifierURL: s.NotifierURL, Enabled: s.Enabled, CreatedAt: s.CreatedAt,
		State: nil,
	}
}

// toSLOWithStateResponse is toSLOResponse + the optional state
// block. Used by the LIST handler in v0.0.23+.
func toSLOWithStateResponse(s slo.WithState) sloResponse {
	r := toSLOResponse(s.SLO)
	if s.HasState {
		var alerted *time.Time
		if !s.State.LastAlertedAt.IsZero() {
			t := s.State.LastAlertedAt
			alerted = &t
		}
		r.State = &sloStateBlock{
			LastEvaluatedAt: s.State.LastEvaluatedAt,
			LastStatus:      string(s.State.LastStatus),
			LastBurnRate:    s.State.LastBurnRate,
			LastAlertedAt:   alerted,
		}
	}
	return r
}

// listSLOsHandler returns GET /v1/slos. Requires `slos:read`.
//
// Uses ListSLOsWithState so each row carries the latest evaluator
// state (burn rate, status, last evaluated). One round-trip for
// the operator-facing dashboard rather than N+1 follow-up fetches.
func listSLOsHandler(store *slo.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		out, err := store.ListSLOsWithState(r.Context(), u.TenantID)
		if err != nil {
			http.Error(w, "list slos: "+err.Error(), http.StatusInternalServerError)
			return
		}
		resp := make([]sloResponse, len(out))
		for i, s := range out {
			resp[i] = toSLOWithStateResponse(s)
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

// createSLOHandler returns POST /v1/slos. Requires `slos:write`.
func createSLOHandler(store *slo.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req sloRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		// Defaults.
		fastT := req.FastBurnThreshold
		if fastT <= 0 {
			fastT = 14.4
		}
		slowT := req.SlowBurnThreshold
		if slowT <= 0 {
			slowT = 6.0
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		id, err := newPrefixedID("slo")
		if err != nil {
			http.Error(w, "id gen: "+err.Error(), http.StatusInternalServerError)
			return
		}
		in := slo.SLO{
			ID: id, TenantID: u.TenantID,
			Name: req.Name, Description: req.Description,
			SLIKind:           slo.SLIKind(req.SLIKind),
			SLIFilter:         req.SLIFilter,
			ObjectivePct:      req.ObjectivePct,
			WindowSeconds:     req.WindowSeconds,
			FastBurnThreshold: fastT,
			SlowBurnThreshold: slowT,
			NotifierURL:       req.NotifierURL,
			Enabled:           enabled,
		}
		out, err := store.CreateSLO(r.Context(), in)
		if err != nil {
			if errors.Is(err, slo.ErrInvalidSLO) || errors.Is(err, slo.ErrUnsupportedSLI) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "create slo: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, toSLOResponse(out))
	})
}

// getSLOHandler returns GET /v1/slos/{id}.
func getSLOHandler(store *slo.Store) http.Handler {
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
		out, err := store.GetSLO(r.Context(), u.TenantID, id)
		if err != nil {
			if errors.Is(err, slo.ErrSLONotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get slo: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toSLOResponse(out))
	})
}

// deleteSLOHandler returns DELETE /v1/slos/{id}.
func deleteSLOHandler(store *slo.Store) http.Handler {
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
		if err := store.DeleteSLO(r.Context(), u.TenantID, id); err != nil {
			if errors.Is(err, slo.ErrSLONotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "delete slo: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
