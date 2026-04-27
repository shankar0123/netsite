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
	"errors"
	"time"
)

// Role enumerates the access tiers in Phase 0. Stored as TEXT in
// Postgres with a CHECK constraint matching this set.
//
// admin    — can do anything, including creating users.
// operator — can write to operational state (canaries, SLOs, alerts).
// viewer   — read-only.
type Role string

// Canonical Role values. The Postgres CHECK constraint on users.role
// must remain in sync with this set; adding a new role requires a
// new migration plus an RBAC policy update.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// Valid reports whether r is one of the canonical roles. Used by
// CreateUser to fail fast on garbage input rather than relying on the
// Postgres CHECK constraint to surface the same error in a noisier way.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	default:
		return false
	}
}

// Tenant is the multi-tenancy root. Even single-tenant deployments
// always have at least one Tenant row so the FK chain stays consistent
// for an acquirer-operated multi-tenant offering later.
type Tenant struct {
	ID        string    // tnt-<slug>
	Name      string    // human-readable display name
	CreatedAt time.Time // UTC
}

// User is a principal — an email + password + role within a Tenant.
type User struct {
	ID        string // usr-<short>
	TenantID  string // tnt-<slug>
	Email     string // citext-stored, case-insensitive
	Role      Role
	CreatedAt time.Time
}

// Session is an opaque server-side session record. The ID is delivered
// to the browser as a cookie value; the server keeps the rest. Tokens
// are unguessable (16 cryptographically random bytes hex-encoded).
type Session struct {
	ID        string    // ses-<short>
	UserID    string    // usr-<short>
	ExpiresAt time.Time // UTC
	CreatedAt time.Time // UTC
}

// Sentinel errors. Callers match on these via errors.Is so handlers
// can produce HTTP-correct status codes without substring-grepping
// error strings.
var (
	// ErrInvalidCredentials is returned by Login when the email is
	// unknown or the password does not verify. Both cases collapse to
	// the same error so attackers cannot distinguish "no such user"
	// from "wrong password" via timing or response shape.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")

	// ErrSessionNotFound is returned when a session ID does not exist
	// in the store or has expired.
	ErrSessionNotFound = errors.New("auth: session not found or expired")

	// ErrUserExists is returned by CreateUser when (tenant_id, email)
	// already exists. The caller is expected to surface this as an
	// HTTP 409 Conflict, not a 500.
	ErrUserExists = errors.New("auth: user already exists")

	// ErrInvalidRole is returned by CreateUser when the requested role
	// is not one of admin/operator/viewer.
	ErrInvalidRole = errors.New("auth: invalid role")

	// ErrWeakPassword is returned when a candidate password fails the
	// minimum-length check. Phase 0 only enforces length; richer rules
	// (entropy, breached-password lookup) are a Phase 5 concern.
	ErrWeakPassword = errors.New("auth: password too short")
)

// MinPasswordLength is the floor enforced by CreateUser and
// RotatePassword. NIST SP 800-63B recommends ≥8; we go to 12 to give
// us margin against the dictionary-attack surface a stolen Postgres
// dump exposes. Trade-off: slightly more friction at user creation,
// in exchange for a meaningfully harder offline crack.
const MinPasswordLength = 12
