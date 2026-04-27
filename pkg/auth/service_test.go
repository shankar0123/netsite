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

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// memStore is an in-memory Store implementation used by these unit
// tests. Keeping it inline (rather than under a testdata or testfixture
// directory) makes the test file self-contained and the fake easy to
// audit alongside the assertions.
type memStore struct {
	users    map[string]User
	hashes   map[string]string
	sessions map[string]Session
}

func newMemStore() *memStore {
	return &memStore{
		users:    map[string]User{},
		hashes:   map[string]string{},
		sessions: map[string]Session{},
	}
}

func userKey(tenant, email string) string { return tenant + "|" + strings.ToLower(email) }

func (m *memStore) CreateUser(_ context.Context, u User, hash string) error {
	k := userKey(u.TenantID, u.Email)
	if _, ok := m.hashes[k]; ok {
		return ErrUserExists
	}
	m.users[u.ID] = u
	m.hashes[k] = hash
	return nil
}

func (m *memStore) GetUserForLogin(_ context.Context, tenant, email string) (User, string, error) {
	k := userKey(tenant, email)
	hash, ok := m.hashes[k]
	if !ok {
		return User{}, "", ErrInvalidCredentials
	}
	for _, u := range m.users {
		if u.TenantID == tenant && strings.EqualFold(u.Email, email) {
			return u, hash, nil
		}
	}
	return User{}, "", ErrInvalidCredentials
}

func (m *memStore) GetUserByID(_ context.Context, userID string) (User, error) {
	u, ok := m.users[userID]
	if !ok {
		return User{}, ErrSessionNotFound
	}
	return u, nil
}

func (m *memStore) UpdateUserPassword(_ context.Context, userID, newHash string) error {
	u, ok := m.users[userID]
	if !ok {
		return ErrSessionNotFound
	}
	m.hashes[userKey(u.TenantID, u.Email)] = newHash
	return nil
}

func (m *memStore) CreateSession(_ context.Context, s Session) error {
	m.sessions[s.ID] = s
	return nil
}

func (m *memStore) GetSession(_ context.Context, id string, now time.Time) (Session, User, error) {
	s, ok := m.sessions[id]
	if !ok || s.ExpiresAt.Before(now) {
		return Session{}, User{}, ErrSessionNotFound
	}
	u, ok := m.users[s.UserID]
	if !ok {
		return Session{}, User{}, ErrSessionNotFound
	}
	return s, u, nil
}

func (m *memStore) DeleteSession(_ context.Context, id string) error {
	delete(m.sessions, id)
	return nil
}

// fixedClock returns a constant time, letting expiry assertions be
// exact instead of "approximately now."
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// newTestService builds a Service against a fresh memStore + fixed
// clock + low bcrypt cost (for speed). Returns the Service and the
// store so tests can inspect persisted state directly.
func newTestService(t *testing.T) (*Service, *memStore, time.Time) {
	t.Helper()
	t.Setenv("NETSITE_AUTH_BCRYPT_COST", "10") // fast hashes in unit tests
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	svc := NewService(store, Config{Clock: fixedClock{t: now}, SessionTTL: time.Hour})
	return svc, store, now
}

func TestService_CreateUser_Login_Whoami_Logout(t *testing.T) {
	svc, store, now := newTestService(t)
	ctx := context.Background()

	// Bootstrap: a tenant exists in the underlying repo on the real
	// path; for the in-memory fake we just skip it because users
	// table has no FK to a fake tenants table.

	in := CreateUserInput{
		TenantID: "tnt-acme",
		Email:    "Alice@Example.com", // mixed case to verify normalisation
		Password: strings.Repeat("a", MinPasswordLength),
		Role:     RoleAdmin,
	}
	user, err := svc.CreateUser(ctx, in)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !strings.HasPrefix(user.ID, "usr-") {
		t.Errorf("user.ID = %q; expected usr- prefix", user.ID)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q; want admin", user.Role)
	}

	// Login round-trip with case-different email.
	sess, err := svc.Login(ctx, in.TenantID, strings.ToUpper(in.Email), in.Password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !strings.HasPrefix(sess.ID, "ses-") {
		t.Errorf("sess.ID = %q; expected ses- prefix", sess.ID)
	}
	if !sess.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v; want now+TTL", sess.ExpiresAt)
	}

	// Whoami returns the same user.
	got, err := svc.Whoami(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("Whoami user.ID = %q; want %q", got.ID, user.ID)
	}

	// Logout deletes the session; subsequent Whoami fails.
	if err := svc.Logout(ctx, sess.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := svc.Whoami(ctx, sess.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Whoami post-logout err = %v; want ErrSessionNotFound", err)
	}
	if _, ok := store.sessions[sess.ID]; ok {
		t.Errorf("session still present after logout")
	}
}

func TestService_Login_Errors(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.CreateUser(ctx, CreateUserInput{
		TenantID: "tnt-acme", Email: "u@x.com",
		Password: strings.Repeat("a", MinPasswordLength), Role: RoleViewer,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cases := []struct {
		name     string
		tenant   string
		email    string
		password string
		want     error
	}{
		{"unknown email", "tnt-acme", "nope@x.com", "anything12345", ErrInvalidCredentials},
		{"wrong tenant", "tnt-other", "u@x.com", strings.Repeat("a", MinPasswordLength), ErrInvalidCredentials},
		{"wrong password", "tnt-acme", "u@x.com", "wrongpassword12", ErrInvalidCredentials},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Login(ctx, tc.tenant, tc.email, tc.password)
			if !errors.Is(err, tc.want) {
				t.Errorf("Login err = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestService_CreateUser_Errors(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreateUserInput
		want error
	}{
		{"invalid role", CreateUserInput{TenantID: "tnt-x", Email: "a@x.com", Password: strings.Repeat("a", MinPasswordLength), Role: "owner"}, ErrInvalidRole},
		{"weak password", CreateUserInput{TenantID: "tnt-x", Email: "a@x.com", Password: "short", Role: RoleViewer}, ErrWeakPassword},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateUser(ctx, tc.in)
			if !errors.Is(err, tc.want) {
				t.Errorf("CreateUser err = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestService_RotatePassword(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	pw := strings.Repeat("a", MinPasswordLength)
	user, err := svc.CreateUser(ctx, CreateUserInput{
		TenantID: "tnt-x", Email: "a@x.com", Password: pw, Role: RoleOperator,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	newPW := strings.Repeat("b", MinPasswordLength)
	if err := svc.RotatePassword(ctx, user.ID, pw, newPW); err != nil {
		t.Fatalf("RotatePassword: %v", err)
	}

	// Old password no longer logs in.
	if _, err := svc.Login(ctx, user.TenantID, user.Email, pw); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("login with old password err = %v; want ErrInvalidCredentials", err)
	}
	// New password logs in.
	if _, err := svc.Login(ctx, user.TenantID, user.Email, newPW); err != nil {
		t.Errorf("login with new password: %v", err)
	}

	// Wrong "current" password fails RotatePassword.
	if err := svc.RotatePassword(ctx, user.ID, "wrongwrong12", strings.Repeat("c", MinPasswordLength)); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("rotate with wrong current err = %v; want ErrInvalidCredentials", err)
	}
}
