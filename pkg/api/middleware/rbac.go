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

package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/shankar0123/netsite/pkg/auth"
)

// What: HTTP middleware that authenticates a request from its session
// cookie and authorizes it against an action string.
//
// How: Authenticate(svc) extracts the session ID from the cookie,
// resolves it to a User, and stuffs the User into the request context.
// Subsequent handlers retrieve the User via UserFromContext. Authorize
// is a per-route wrapper that calls auth.Authorize(role, action) and
// returns 403 on deny.
//
// Why two layers (Authenticate + Authorize): Authenticate runs once
// per request before route dispatch; Authorize is per-route because
// each route needs a different action string. Combining them would
// force every route to also know how to look up sessions — inverting
// the layering.

// Authenticator is the narrow slice of auth.Service the middleware
// needs. Defining the interface here keeps tests independent of the
// full Service.
type Authenticator interface {
	Whoami(ctx context.Context, sessionID string) (auth.User, error)
}

// userCtxKey is a private context key to avoid collisions with other
// packages' contexts.
type userCtxKey struct{}

// WithUser returns ctx with u attached. Exported for tests.
func WithUser(ctx context.Context, u auth.User) context.Context {
	return context.WithValue(ctx, userCtxKey{}, u)
}

// UserFromContext returns the authenticated user attached by
// Authenticate. ok is false when the request is anonymous.
func UserFromContext(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userCtxKey{}).(auth.User)
	return u, ok
}

// Authenticate wraps next with a layer that resolves the session
// cookie (if any) to a User and attaches it to the request context.
// Anonymous requests pass through with no User in context — Authorize
// will deny them at the next layer.
//
// We deliberately do NOT 401 here. Some routes (login, /v1/health,
// /metrics) are anonymous; gating those at this layer would break
// them. Authorize is the gate.
func Authenticate(svc Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := auth.SessionIDFromRequest(r)
		if sid == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := svc.Whoami(r.Context(), sid)
		if err != nil {
			// Treat any session-resolution error as anonymous; let
			// Authorize decide whether the route requires a user.
			// We do NOT clear the cookie here — the client may have
			// a valid session for a different host or it may be a
			// transient error. Logout is the right path to clear it.
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// Authorize wraps next so that requests reaching it must (a) have an
// authenticated user in context and (b) have a role that satisfies
// `action`. Non-authenticated requests get 401; authorization failures
// get 403.
//
// Use:
//
//	mux.Handle("POST /v1/tests", Authorize("tests:write", testsCreate))
func Authorize(action string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := auth.Authorize(u.Role, action); err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.Error(w, "internal authorization error", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r)
	})
}
