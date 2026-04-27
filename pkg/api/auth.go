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
	"encoding/json"
	"errors"
	"net/http"

	"github.com/shankar0123/netsite/pkg/api/middleware"
	"github.com/shankar0123/netsite/pkg/auth"
)

// What: HTTP handlers for /v1/auth/{login,logout,whoami}.
//
// How: each handler is a closure over an `authService` interface, the
// narrow slice of auth.Service the handlers actually need. Login
// expects JSON body {tenant_id, email, password}; on success, sets
// the session cookie and returns the user. Logout clears the cookie
// and deletes the session. Whoami requires Authenticate middleware to
// have already attached the user to the request context.
//
// Why a request/response struct per handler: the OpenAPI spec is the
// canonical contract; structs map directly to schemas there. Keeps
// the JSON shape stable across refactors and makes adding fields
// (e.g. MFA challenge in Phase 5) a one-line struct change.

// authService is the dependency surface auth handlers expect. The
// concrete type satisfies it via *auth.Service.
type authService interface {
	Login(ctx context.Context, tenantID, email, password string) (auth.Session, error)
	Logout(ctx context.Context, sessionID string) error
	Whoami(ctx context.Context, sessionID string) (auth.User, error)
}

// loginRequest is the JSON body of POST /v1/auth/login.
type loginRequest struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// userResponse is the JSON shape returned on Login and Whoami.
// We deliberately do NOT include the session ID here; it lives in
// the cookie. Echoing it in the body invites client-side handling
// that bypasses the cookie's HttpOnly protection.
type userResponse struct {
	ID       string    `json:"id"`
	TenantID string    `json:"tenant_id"`
	Email    string    `json:"email"`
	Role     auth.Role `json:"role"`
}

// loginHandler returns a handler for POST /v1/auth/login.
func loginHandler(svc authService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.TenantID == "" || req.Email == "" || req.Password == "" {
			http.Error(w, "tenant_id, email, and password are required", http.StatusBadRequest)
			return
		}
		sess, err := svc.Login(r.Context(), req.TenantID, req.Email, req.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Resolve the user once via Whoami so the response body is
		// the same shape Whoami returns. Saves the client a round
		// trip after login.
		user, err := svc.Whoami(r.Context(), sess.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		auth.SetSessionCookie(w, sess)
		writeJSON(w, http.StatusOK, userResponse{
			ID: user.ID, TenantID: user.TenantID, Email: user.Email, Role: user.Role,
		})
	})
}

// logoutHandler returns a handler for POST /v1/auth/logout.
// Idempotent: logging out an already-anonymous session is a no-op.
func logoutHandler(svc authService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := auth.SessionIDFromRequest(r)
		_ = svc.Logout(r.Context(), sid)
		auth.ClearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	})
}

// whoamiHandler returns a handler for GET /v1/auth/whoami. Requires
// the Authenticate middleware to have populated the user in context;
// returns 401 if no user is present.
func whoamiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, userResponse{
			ID: u.ID, TenantID: u.TenantID, Email: u.Email, Role: u.Role,
		})
	})
}

// writeJSON marshals body to JSON and writes it with the given status.
// Errors during marshal land in the slog request log via the logging
// middleware's recovery wrapper; the client sees an empty body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
