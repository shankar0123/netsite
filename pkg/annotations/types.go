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

// Package annotations is NetSite's pinned-note primitive. Operators
// attach a tiny markdown body to a (scope, scope_id, timestamp)
// tuple — typically a canary failure point, a POP-wide event, or a
// test-config change. The dashboard renders annotations as markers
// on whichever timeline matches their scope.
//
// What:
//   - Annotation: id + tenant + (scope, scope_id, at) + author +
//     body_md + created_at.
//   - Scope: a small enum identifying the kind of object the
//     annotation hangs off (canary | pop | test in v0.0.12; more
//     scopes added per phase as they ship).
//   - Validate / sentinel errors for handler-side fast-fail.
//
// How: types.go declares the model; store.go is the pgxpool-backed
// CRUD. Service-layer logic is so thin (mint id, validate, hand
// off) that we expose a small Service struct here in types.go
// rather than splitting it into its own file.
//
// Why immutable: an annotation's role is to record what an operator
// noted at a moment in time. Mutating the body would invalidate the
// audit trail. Operators correct typos by deleting + recreating.
package annotations

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Scope is the kind of object an annotation hangs off.
type Scope string

// Canonical Scope values in v0.0.12.
const (
	ScopeCanary Scope = "canary"
	ScopePOP    Scope = "pop"
	ScopeTest   Scope = "test"
)

// validScopes is the lookup the validator uses. Keep in sync with
// the CHECK constraint in 0007_annotations.sql.
var validScopes = map[Scope]struct{}{
	ScopeCanary: {},
	ScopePOP:    {},
	ScopeTest:   {},
}

// Annotation is one pinned note.
type Annotation struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Scope     Scope     `json:"scope"`
	ScopeID   string    `json:"scope_id"`
	At        time.Time `json:"at"`
	BodyMD    string    `json:"body_md"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateRequest is the input shape for Service.Create. Pulled out
// of Annotation so the handler doesn't accept caller-supplied IDs
// or timestamps.
type CreateRequest struct {
	Scope   Scope     `json:"scope"`
	ScopeID string    `json:"scope_id"`
	At      time.Time `json:"at"`
	BodyMD  string    `json:"body_md"`
}

// ListFilter narrows a List query. All fields except TenantID
// (which the caller always supplies for security) are optional.
//
// Why a struct over positional args: the filter dimensions grow as
// new scopes ship; struct shape lets callers extend without
// rewriting every call site.
type ListFilter struct {
	Scope    Scope      // empty → match any scope
	ScopeID  string     // empty → match any id within Scope
	From, To *time.Time // optional time-range bounds
	Limit    int        // 0 → DefaultListLimit
}

// Default constants.
const (
	// MaxBodyLen caps the markdown payload. Generous enough for any
	// real postmortem note; tight enough that a runaway client
	// can't post a megabyte of text.
	MaxBodyLen = 4000
	// MaxScopeIDLen reflects the prefixed-TEXT IDs used everywhere.
	MaxScopeIDLen = 200
	// DefaultListLimit caps List output when the caller omits Limit.
	// Operators reading a single canary's timeline rarely want more
	// than a screenful; 200 is well above that threshold.
	DefaultListLimit = 200
	// MaxListLimit is the upper bound a caller can request. Cap is
	// enforced in the handler so a runaway client can't OOM the API.
	MaxListLimit = 1000
)

// Sentinel errors. Callers use errors.Is to distinguish.
var (
	ErrInvalidAnnotation  = errors.New("annotations: invalid annotation")
	ErrAnnotationNotFound = errors.New("annotations: not found")
)

// ValidateScope reports whether s is in the v0.0.12 scope set. The
// validator runs server-side; the CHECK constraint in the migration
// is the second line of defense.
func ValidateScope(s Scope) error {
	if _, ok := validScopes[s]; !ok {
		return fmt.Errorf("scope %q is not one of canary/pop/test", s)
	}
	return nil
}

// ValidateScopeID enforces the trim+length rules.
func ValidateScopeID(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("scope_id is required")
	}
	if len(s) > MaxScopeIDLen {
		return errors.New("scope_id exceeds max length")
	}
	return nil
}

// ValidateBody enforces the body's length cap. We intentionally
// allow empty bodies — operators sometimes pin "saw something"
// markers that get filled in later.
func ValidateBody(s string) error {
	if len(s) > MaxBodyLen {
		return errors.New("body exceeds max length")
	}
	return nil
}

// Reader is the read-side persistence interface the Service depends on.
type Reader interface {
	Get(ctx context.Context, tenantID, id string) (Annotation, error)
	List(ctx context.Context, tenantID string, f ListFilter) ([]Annotation, error)
}

// Mutator is the write-side persistence interface.
type Mutator interface {
	Insert(ctx context.Context, a Annotation) (Annotation, error)
	Delete(ctx context.Context, tenantID, id string) error
}

// Clock returns the current UTC time. Tests inject fixed clocks.
type Clock func() time.Time

// IDGen mints a fresh annotation ID. Tests inject deterministic ones.
type IDGen func() string

// Service is the business-logic facade — validates input, mints
// IDs, stamps timestamps, and delegates to the Reader/Mutator.
type Service struct {
	r      Reader
	m      Mutator
	now    Clock
	mintID IDGen
}

// Options is the dependency bag for NewService. Zero values fall
// back to production defaults.
type Options struct {
	Now    Clock
	MintID IDGen
}

// NewService wires a Service.
func NewService(r Reader, m Mutator, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.MintID == nil {
		opts.MintID = defaultIDGen
	}
	return &Service{r: r, m: m, now: opts.Now, mintID: opts.MintID}
}

// Create validates the request and persists.
func (s *Service) Create(ctx context.Context, tenantID, authorID string, req CreateRequest) (Annotation, error) {
	if err := ValidateScope(req.Scope); err != nil {
		return Annotation{}, fmt.Errorf("%w: %v", ErrInvalidAnnotation, err)
	}
	if err := ValidateScopeID(req.ScopeID); err != nil {
		return Annotation{}, fmt.Errorf("%w: %v", ErrInvalidAnnotation, err)
	}
	if err := ValidateBody(req.BodyMD); err != nil {
		return Annotation{}, fmt.Errorf("%w: %v", ErrInvalidAnnotation, err)
	}
	if req.At.IsZero() {
		return Annotation{}, fmt.Errorf("%w: at is required", ErrInvalidAnnotation)
	}
	a := Annotation{
		ID:       s.mintID(),
		TenantID: tenantID,
		Scope:    req.Scope,
		ScopeID:  req.ScopeID,
		At:       req.At.UTC(),
		BodyMD:   req.BodyMD,
		AuthorID: authorID,
	}
	return s.m.Insert(ctx, a)
}

// Get is a thin pass-through.
func (s *Service) Get(ctx context.Context, tenantID, id string) (Annotation, error) {
	return s.r.Get(ctx, tenantID, id)
}

// List applies a filter. Bounds the limit.
func (s *Service) List(ctx context.Context, tenantID string, f ListFilter) ([]Annotation, error) {
	if f.Limit <= 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit > MaxListLimit {
		f.Limit = MaxListLimit
	}
	if f.Scope != "" {
		if err := ValidateScope(f.Scope); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAnnotation, err)
		}
	}
	return s.r.List(ctx, tenantID, f)
}

// Delete removes one annotation.
func (s *Service) Delete(ctx context.Context, tenantID, id string) error {
	return s.m.Delete(ctx, tenantID, id)
}

// defaultIDGen mints `ann-<8 hex chars>` from crypto/rand.
func defaultIDGen() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand reads don't fail in normal operation; when
		// they do, crashing is preferable to silently emitting
		// degenerate IDs.
		panic(fmt.Errorf("annotations: crypto/rand: %w", err))
	}
	return fmt.Sprintf("ann-%x", buf)
}
