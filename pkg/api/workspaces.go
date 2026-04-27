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
	"github.com/shankar0123/netsite/pkg/workspaces"
)

// What: HTTP handlers for /v1/workspaces and the public
// /v1/share/{slug}. Same shape as pkg/api/slos.go — closures over a
// service, tenant scoping from the authenticated user, RBAC at the
// route registration site.
//
// How: each handler validates input, calls the service, maps known
// errors to HTTP status codes, and writes JSON. The share-resolve
// handler is the only one that bypasses authentication — share
// links are public reads by design (the slug is the secret).
//
// Why a *workspaces.Service injected via Config rather than a
// store: validation, ID minting, and slug generation belong on the
// service. Handlers stay thin so the unit tests for the business
// logic don't have to spin up an HTTP layer.

// workspaceCreateRequest is the body of POST /v1/workspaces.
type workspaceCreateRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Views       []workspaces.View `json:"views,omitempty"`
}

// workspaceUpdateRequest is the body of PATCH /v1/workspaces/{id}.
// All fields are optional; nil pointers keep the existing value.
type workspaceUpdateRequest struct {
	Name        *string            `json:"name,omitempty"`
	Description *string            `json:"description,omitempty"`
	Views       *[]workspaces.View `json:"views,omitempty"`
}

// workspaceShareRequest is the body of POST /v1/workspaces/{id}/share.
// TTLSeconds is optional; zero falls back to workspaces.DefaultShareTTL.
type workspaceShareRequest struct {
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// The response type is workspaces.Workspace itself — we deliberately
// don't redeclare it here because the JSON tags on the source type
// already match what the API surface needs. Tests assert this by
// round-tripping through encoding/json.

// listWorkspacesHandler is GET /v1/workspaces.
func listWorkspacesHandler(svc *workspaces.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		out, err := svc.List(r.Context(), u.TenantID)
		if err != nil {
			http.Error(w, "list workspaces: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// createWorkspaceHandler is POST /v1/workspaces.
func createWorkspaceHandler(svc *workspaces.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req workspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		out, err := svc.Create(r.Context(), u.TenantID, u.ID, workspaces.CreateRequest{
			Name:        req.Name,
			Description: req.Description,
			Views:       req.Views,
		})
		if err != nil {
			if errors.Is(err, workspaces.ErrInvalidWorkspace) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "create workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	})
}

// getWorkspaceHandler is GET /v1/workspaces/{id}.
func getWorkspaceHandler(svc *workspaces.Service) http.Handler {
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
		out, err := svc.Get(r.Context(), u.TenantID, id)
		if err != nil {
			if errors.Is(err, workspaces.ErrWorkspaceNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// updateWorkspaceHandler is PATCH /v1/workspaces/{id}.
func updateWorkspaceHandler(svc *workspaces.Service) http.Handler {
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
		var req workspaceUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		out, err := svc.Update(r.Context(), u.TenantID, id, workspaces.UpdateRequest{
			Name:        req.Name,
			Description: req.Description,
			Views:       req.Views,
		})
		if err != nil {
			switch {
			case errors.Is(err, workspaces.ErrWorkspaceNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, workspaces.ErrInvalidWorkspace):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				http.Error(w, "update workspace: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// deleteWorkspaceHandler is DELETE /v1/workspaces/{id}.
func deleteWorkspaceHandler(svc *workspaces.Service) http.Handler {
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
		if err := svc.Delete(r.Context(), u.TenantID, id); err != nil {
			if errors.Is(err, workspaces.ErrWorkspaceNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "delete workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// shareWorkspaceHandler is POST /v1/workspaces/{id}/share. Mints a
// fresh slug + expiry; returns the workspace with the slug populated.
// Calling Share twice rotates the slug — the previous one is
// effectively revoked.
func shareWorkspaceHandler(svc *workspaces.Service) http.Handler {
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
		var req workspaceShareRequest
		// Accept an empty body — the zero TTL falls back to the default.
		_ = json.NewDecoder(r.Body).Decode(&req)
		ttl := time.Duration(req.TTLSeconds) * time.Second
		out, err := svc.Share(r.Context(), u.TenantID, id, workspaces.ShareOptions{TTL: ttl})
		if err != nil {
			switch {
			case errors.Is(err, workspaces.ErrWorkspaceNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, workspaces.ErrInvalidWorkspace):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				http.Error(w, "share workspace: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// unshareWorkspaceHandler is DELETE /v1/workspaces/{id}/share.
func unshareWorkspaceHandler(svc *workspaces.Service) http.Handler {
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
		out, err := svc.Unshare(r.Context(), u.TenantID, id)
		if err != nil {
			if errors.Is(err, workspaces.ErrWorkspaceNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "unshare workspace: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// resolveShareHandler is GET /v1/share/{slug}. Public — no auth.
// The slug itself is the access control. We deliberately do NOT
// expose tenant_id or owner_user_id in the response here; the
// share is meant to be a read-only deep link.
func resolveShareHandler(svc *workspaces.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if slug == "" {
			http.Error(w, "slug required", http.StatusBadRequest)
			return
		}
		out, err := svc.Resolve(r.Context(), slug)
		if err != nil {
			if errors.Is(err, workspaces.ErrShareNotFound) {
				http.Error(w, "not found or expired", http.StatusNotFound)
				return
			}
			http.Error(w, "resolve share: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Strip tenant + owner before returning. Leaking those over
		// a public link would tell an outside observer who owns the
		// share even if they can't see anything else.
		safe := workspaces.Workspace{
			ID:             out.ID,
			Name:           out.Name,
			Description:    out.Description,
			Views:          out.Views,
			ShareSlug:      out.ShareSlug,
			ShareExpiresAt: out.ShareExpiresAt,
			CreatedAt:      out.CreatedAt,
			UpdatedAt:      out.UpdatedAt,
		}
		writeJSON(w, http.StatusOK, safe)
	})
}
