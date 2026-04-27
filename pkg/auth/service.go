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
	"fmt"
	"time"
)

// What: the public auth API. Handlers depend on Service; Service
// depends on Store and Clock; both have testable in-memory variants.
//
// How: Login looks up the user, verifies the password, mints a fresh
// session, persists it. Logout deletes the session. Whoami resolves a
// session ID to a user. CreateUser/RotatePassword run the password
// validation pipeline (length → bcrypt) before touching the store.
//
// Why a Service interface and a concrete struct: handlers want a
// narrow surface they can mock; ns-controlplane wants the concrete
// thing. The interface lives here so service_test.go can stub it
// without circular imports between pkg/auth and pkg/api.

// Store is the persistence interface Service depends on. The Postgres
// implementation lives in repo.go; in-memory fakes for unit tests
// live in service_test.go.
type Store interface {
	CreateUser(ctx context.Context, u User, passwordHash string) error
	GetUserForLogin(ctx context.Context, tenantID, email string) (User, string, error)
	GetUserByID(ctx context.Context, userID string) (User, error)
	UpdateUserPassword(ctx context.Context, userID, newHash string) error

	CreateSession(ctx context.Context, s Session) error
	GetSession(ctx context.Context, sessionID string, now time.Time) (Session, User, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

// Clock is the time abstraction the Service uses. Tests inject a
// frozen Clock so session-expiry assertions are deterministic.
//
// Why our own interface, not an external dependency: this is two
// methods (Now, Add). A package-internal interface keeps the
// dependency graph clean and matches the CLAUDE.md "stdlib first"
// stance.
type Clock interface {
	Now() time.Time
}

// realClock is the production Clock that calls time.Now().
type realClock struct{}

// Now returns the current UTC time.
func (realClock) Now() time.Time { return time.Now().UTC() }

// Service is the auth API. Construct with NewService.
type Service struct {
	store    Store
	clock    Clock
	sessTTL  time.Duration
	idGenSes func() (string, error)
	idGenUsr func() (string, error)
}

// Config holds NewService inputs that have sensible defaults.
type Config struct {
	// SessionTTL is how long a freshly minted session stays valid.
	// Zero means use the default of 12 hours.
	SessionTTL time.Duration
	// Clock lets tests inject a frozen time source. Zero means use
	// the real wall clock.
	Clock Clock
}

// defaultSessionTTL bounds how long a session lives. 12 hours mirrors
// what most operator dashboards default to: long enough that an
// engineer doing a half-day of work doesn't re-auth, short enough
// that a stolen cookie has bounded blast radius.
const defaultSessionTTL = 12 * time.Hour

// NewService wires a Service. The Store is required; everything else
// has a default.
func NewService(store Store, cfg Config) *Service {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return &Service{
		store:    store,
		clock:    cfg.Clock,
		sessTTL:  cfg.SessionTTL,
		idGenSes: NewSessionID,
		idGenUsr: NewUserID,
	}
}

// Login authenticates a (tenantID, email, password) and returns a new
// Session on success. ErrInvalidCredentials is returned for any
// failure of the lookup-or-verify pipeline so attackers cannot tell
// "no such user" from "wrong password."
func (s *Service) Login(ctx context.Context, tenantID, email, password string) (Session, error) {
	user, hash, err := s.store.GetUserForLogin(ctx, tenantID, email)
	if err != nil {
		return Session{}, err
	}
	if err := VerifyPassword(hash, password); err != nil {
		return Session{}, ErrInvalidCredentials
	}
	id, err := s.idGenSes()
	if err != nil {
		return Session{}, fmt.Errorf("auth: gen session id: %w", err)
	}
	now := s.clock.Now()
	sess := Session{
		ID:        id,
		UserID:    user.ID,
		ExpiresAt: now.Add(s.sessTTL),
		CreatedAt: now,
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Logout deletes the session row. Idempotent: deleting a non-existent
// session is not an error so a double-logout (e.g. user clicks twice
// on a slow connection) does not surface a confusing 404.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, sessionID)
}

// Whoami resolves a session ID to the authenticated User. Returns
// ErrSessionNotFound if the session is missing or expired.
func (s *Service) Whoami(ctx context.Context, sessionID string) (User, error) {
	if sessionID == "" {
		return User{}, ErrSessionNotFound
	}
	_, user, err := s.store.GetSession(ctx, sessionID, s.clock.Now())
	if err != nil {
		return User{}, err
	}
	return user, nil
}

// CreateUserInput collects the parameters of CreateUser. Wrapping in
// a struct keeps the call site readable and lets us add fields
// without breaking callers (e.g., display name in Phase 1).
type CreateUserInput struct {
	TenantID string
	Email    string
	Password string
	Role     Role
}

// CreateUser validates input, hashes the password, and persists the
// user. Returns the persisted User on success.
//
// Errors:
//   - ErrInvalidRole if Role is not admin/operator/viewer.
//   - ErrWeakPassword if Password is shorter than MinPasswordLength.
//   - ErrUserExists if (TenantID, Email) already exists.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (User, error) {
	if !in.Role.Valid() {
		return User{}, ErrInvalidRole
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	id, err := s.idGenUsr()
	if err != nil {
		return User{}, fmt.Errorf("auth: gen user id: %w", err)
	}
	u := User{
		ID:        id,
		TenantID:  in.TenantID,
		Email:     in.Email,
		Role:      in.Role,
		CreatedAt: s.clock.Now(),
	}
	if err := s.store.CreateUser(ctx, u, hash); err != nil {
		return User{}, err
	}
	return u, nil
}

// RotatePassword verifies the user's current password and replaces it
// with newPassword. Returns ErrInvalidCredentials if the current
// password is wrong, ErrWeakPassword if newPassword fails the floor.
func (s *Service) RotatePassword(ctx context.Context, userID, current, newPassword string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	// We need the password hash to verify; GetUserForLogin returns
	// it. Use the email + tenant we already know to fetch.
	_, hash, err := s.store.GetUserForLogin(ctx, user.TenantID, user.Email)
	if err != nil {
		return err
	}
	if err := VerifyPassword(hash, current); err != nil {
		return ErrInvalidCredentials
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.store.UpdateUserPassword(ctx, userID, newHash)
}
