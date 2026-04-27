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
	"strconv"
	"time"

	"github.com/shankar0123/netsite/pkg/annotations"
	"github.com/shankar0123/netsite/pkg/api/middleware"
)

// What: HTTP handlers for /v1/annotations. Same shape as
// pkg/api/{slos,workspaces}.go — closures over a Service, tenant
// scoping from the authenticated user, RBAC at the route
// registration site.
//
// How: each handler validates input, calls the service, maps known
// errors to HTTP status codes, and writes JSON.
//
// Why no PATCH: annotations are immutable by design — the audit
// trail relies on it. A typo correction is delete + create, not
// edit.

// listAnnotationsHandler is GET /v1/annotations. Query-string
// filters: scope, scope_id, from, to, limit. Empty values match
// any.
//
// Time format for from/to: RFC3339 (`2026-04-27T12:30:00Z`). We
// reject unparseable values rather than silently ignoring them; an
// operator who got the format wrong needs to know.
func listAnnotationsHandler(svc *annotations.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		f := annotations.ListFilter{
			Scope:   annotations.Scope(q.Get("scope")),
			ScopeID: q.Get("scope_id"),
		}
		if s := q.Get("from"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "bad from: "+err.Error(), http.StatusBadRequest)
				return
			}
			f.From = &t
		}
		if s := q.Get("to"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "bad to: "+err.Error(), http.StatusBadRequest)
				return
			}
			f.To = &t
		}
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 {
				http.Error(w, "bad limit", http.StatusBadRequest)
				return
			}
			f.Limit = n
		}
		out, err := svc.List(r.Context(), u.TenantID, f)
		if err != nil {
			if errors.Is(err, annotations.ErrInvalidAnnotation) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "list annotations: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// createAnnotationHandler is POST /v1/annotations.
func createAnnotationHandler(svc *annotations.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req annotations.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		out, err := svc.Create(r.Context(), u.TenantID, u.ID, req)
		if err != nil {
			if errors.Is(err, annotations.ErrInvalidAnnotation) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "create annotation: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	})
}

// getAnnotationHandler is GET /v1/annotations/{id}.
func getAnnotationHandler(svc *annotations.Service) http.Handler {
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
			if errors.Is(err, annotations.ErrAnnotationNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get annotation: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// deleteAnnotationHandler is DELETE /v1/annotations/{id}.
func deleteAnnotationHandler(svc *annotations.Service) http.Handler {
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
			if errors.Is(err, annotations.ErrAnnotationNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			http.Error(w, "delete annotation: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
