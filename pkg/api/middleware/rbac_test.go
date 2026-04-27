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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shankar0123/netsite/pkg/auth"
)

// fakeAuth returns a fixed (User, error) regardless of input. Lets us
// drive Authenticate without spinning up the real Service.
type fakeAuth struct {
	user auth.User
	err  error
}

func (f fakeAuth) Whoami(_ context.Context, _ string) (auth.User, error) {
	return f.user, f.err
}

// echoOK returns 200 with body "ok". Used to verify pass-through.
var echoOK = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

// TestAuthenticate_AnonymousPassthrough asserts a request with no
// session cookie passes through with no user attached.
func TestAuthenticate_AnonymousPassthrough(t *testing.T) {
	saw := false
	innerH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := UserFromContext(r.Context())
		if ok {
			t.Errorf("expected no user; got one")
		}
		saw = true
		w.WriteHeader(http.StatusOK)
	})
	h := Authenticate(fakeAuth{}, innerH)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !saw {
		t.Fatal("inner handler not called")
	}
}

// TestAuthenticate_ValidSession attaches the cookie, asserts that the
// inner handler sees the user via UserFromContext.
func TestAuthenticate_ValidSession(t *testing.T) {
	want := auth.User{ID: "usr-1", Role: auth.RoleAdmin}
	innerH := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok := UserFromContext(r.Context())
		if !ok {
			t.Fatal("expected user in context")
		}
		if got.ID != want.ID {
			t.Errorf("user ID = %q; want %q", got.ID, want.ID)
		}
	})
	h := Authenticate(fakeAuth{user: want}, innerH)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "ses-x"})
	h.ServeHTTP(rec, req)
}

// TestAuthenticate_BadSessionTreatedAnonymous asserts a session
// resolution error does not block the request — Authorize is the
// gate, not Authenticate.
func TestAuthenticate_BadSessionTreatedAnonymous(t *testing.T) {
	innerH := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); ok {
			t.Error("expected no user when Whoami errored")
		}
	})
	h := Authenticate(fakeAuth{err: auth.ErrSessionNotFound}, innerH)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "ses-x"})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Errorf("status = %d; want pass-through 200/0", rec.Code)
	}
}

// TestAuthorize_Matrix walks every (role, action) cell against the
// guard wrapper. Same matrix as auth/rbac_test.go but exercising the
// HTTP layer's behaviour (status codes).
func TestAuthorize_Matrix(t *testing.T) {
	cases := []struct {
		name       string
		user       *auth.User // nil = anonymous
		action     string
		wantStatus int
	}{
		{"anonymous denied 401", nil, "tests:read", http.StatusUnauthorized},
		{"viewer reads ok", &auth.User{Role: auth.RoleViewer}, "tests:read", http.StatusOK},
		{"viewer write 403", &auth.User{Role: auth.RoleViewer}, "tests:write", http.StatusForbidden},
		{"operator reads ok", &auth.User{Role: auth.RoleOperator}, "tests:read", http.StatusOK},
		{"operator writes ok", &auth.User{Role: auth.RoleOperator}, "tests:write", http.StatusOK},
		{"operator admin 403", &auth.User{Role: auth.RoleOperator}, "tests:admin", http.StatusForbidden},
		{"admin admin ok", &auth.User{Role: auth.RoleAdmin}, "tests:admin", http.StatusOK},
		{"unknown role 403", &auth.User{Role: auth.Role("intern")}, "tests:read", http.StatusForbidden},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := Authorize(tc.action, echoOK)
			ctx := context.Background()
			if tc.user != nil {
				ctx = WithUser(ctx, *tc.user)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d; want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}
