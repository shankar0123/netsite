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
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What: Postgres-backed implementation of the persistence interface
// the Service depends on. Tables are defined in 0003_auth_core.sql.
//
// How: a single Repo struct wraps a *pgxpool.Pool and exposes a small
// set of methods (CreateUser, GetUserForLogin, CreateSession,
// GetSession, DeleteSession, GetUserByID, UpdateUserPassword,
// EnsureTenant). The Service layer composes these into the public
// auth API.
//
// Why one Repo struct vs. separate UsersRepo / SessionsRepo: at v0
// scale the split is overhead — every method here is two lines of SQL.
// We keep one struct so the constructor wires once and Service does
// not thread two dependencies. If/when the SQL grows organically, we
// split.

// Repo is a Postgres-backed auth store.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps an already-open pgxpool.Pool. The caller is
// responsible for the pool's lifecycle (Close). This makes the Repo
// trivially testable: pass any pool, including one bound to a
// testcontainers Postgres for integration tests.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// pgUniqueViolation is the SQLSTATE Postgres returns when an INSERT
// violates a UNIQUE constraint. We map it to ErrUserExists so the
// service layer can return HTTP 409 without parsing error strings.
const pgUniqueViolation = "23505"

// EnsureTenant inserts a tenant row idempotently. If a row with the
// same id already exists, it is left in place. Used by the seed
// command to bootstrap a tenant on a fresh database.
func (r *Repo) EnsureTenant(ctx context.Context, t Tenant) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO tenants (id, name)
		VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, t.ID, t.Name)
	if err != nil {
		return fmt.Errorf("auth: ensure tenant %q: %w", t.ID, err)
	}
	return nil
}

// CreateUser inserts a new user row. Returns ErrUserExists if
// (tenant_id, email) already exists.
func (r *Repo) CreateUser(ctx context.Context, u User, passwordHash string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
	`, u.ID, u.TenantID, strings.ToLower(u.Email), passwordHash, string(u.Role))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ErrUserExists
		}
		return fmt.Errorf("auth: create user: %w", err)
	}
	return nil
}

// GetUserForLogin returns the user (with password hash) matching the
// given tenant + email. Returns ErrInvalidCredentials when not found
// — never reveal "no such user" vs "wrong password" via this method.
//
// Why pass tenantID: NetSite is multi-tenant; same email can belong
// to two tenants with different password hashes and roles.
func (r *Repo) GetUserForLogin(ctx context.Context, tenantID, email string) (User, string, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, email, role, password_hash, created_at
		FROM users
		WHERE tenant_id = $1 AND email = $2
	`, tenantID, strings.ToLower(email))

	var u User
	var passwordHash string
	if err := row.Scan(&u.ID, &u.TenantID, &u.Email, (*string)(&u.Role), &passwordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, "", ErrInvalidCredentials
		}
		return User{}, "", fmt.Errorf("auth: load user for login: %w", err)
	}
	return u, passwordHash, nil
}

// GetUserByID returns the user with the given id. Used by Whoami.
// Returns ErrSessionNotFound if no row matches — the session refers
// to a deleted user, which we treat as "no longer authenticated."
func (r *Repo) GetUserByID(ctx context.Context, userID string) (User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, tenant_id, email, role, created_at
		FROM users
		WHERE id = $1
	`, userID)

	var u User
	if err := row.Scan(&u.ID, &u.TenantID, &u.Email, (*string)(&u.Role), &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrSessionNotFound
		}
		return User{}, fmt.Errorf("auth: load user by id: %w", err)
	}
	return u, nil
}

// UpdateUserPassword replaces the user's password hash. Returns nil
// on success even if the password is unchanged.
func (r *Repo) UpdateUserPassword(ctx context.Context, userID, newHash string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET password_hash = $1 WHERE id = $2
	`, newHash, userID)
	if err != nil {
		return fmt.Errorf("auth: update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// CreateSession inserts a new session row. The session must already
// have a non-empty ID, UserID, and ExpiresAt — the Service generates
// these.
func (r *Repo) CreateSession(ctx context.Context, s Session) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, s.ID, s.UserID, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("auth: create session: %w", err)
	}
	return nil
}

// GetSession returns the (session, user) pair for sessionID. Returns
// ErrSessionNotFound if the row does not exist or has expired.
func (r *Repo) GetSession(ctx context.Context, sessionID string, now time.Time) (Session, User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.expires_at, s.created_at,
		       u.id, u.tenant_id, u.email, u.role, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > $2
	`, sessionID, now)

	var s Session
	var u User
	if err := row.Scan(
		&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt,
		&u.ID, &u.TenantID, &u.Email, (*string)(&u.Role), &u.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, User{}, ErrSessionNotFound
		}
		return Session{}, User{}, fmt.Errorf("auth: load session: %w", err)
	}
	return s, u, nil
}

// DeleteSession removes a session row. Returns nil even if the row
// does not exist — Logout is idempotent.
func (r *Repo) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}
