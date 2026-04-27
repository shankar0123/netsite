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

package workspaces

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// What: business logic over a Store. Mints prefixed IDs and share
// slugs, validates input, and stamps timestamps via an injected
// Clock so unit tests stay deterministic.
//
// How: the Service composes a Reader interface (a narrow read-side
// view of the Store, easy to fake in unit tests) with a Mutator
// interface (the write-side). NewService accepts both as a single
// concrete *Store; tests can inject in-memory fakes that satisfy
// the same shape.
//
// Why split read and write interfaces: the share-link resolver only
// needs reads; we keep that surface tiny so a future "public read
// service" can satisfy just the Reader without dragging in the
// write methods.

// Reader is the narrow read-side interface the service depends on.
type Reader interface {
	Get(ctx context.Context, tenantID, id string) (Workspace, error)
	List(ctx context.Context, tenantID string) ([]Workspace, error)
	GetByShareSlug(ctx context.Context, slug string, now time.Time) (Workspace, error)
}

// Mutator is the write-side interface.
type Mutator interface {
	Insert(ctx context.Context, w Workspace) (Workspace, error)
	Update(ctx context.Context, tenantID, id string, req UpdateRequest) (Workspace, error)
	Delete(ctx context.Context, tenantID, id string) error
	SetShare(ctx context.Context, tenantID, id string, slug string, expiresAt *time.Time) (Workspace, error)
}

// Clock returns the current UTC time. Tests inject a fixed
// implementation; production wires time.Now().UTC.
type Clock func() time.Time

// IDGen returns a freshly-minted workspace ID like `wks-abc123`.
// Tests inject a deterministic generator.
type IDGen func() string

// SlugGen returns a fresh share slug. Tests inject a deterministic
// generator.
type SlugGen func() (string, error)

// Service is the business-logic facade.
type Service struct {
	r        Reader
	m        Mutator
	now      Clock
	mintID   IDGen
	mintSlug SlugGen
}

// Options is the dependency bag for NewService. Zero values fall
// back to production defaults: time.Now().UTC, prefixed-random IDs
// from crypto/rand, base64url-encoded 16-byte slugs.
type Options struct {
	Now      Clock
	MintID   IDGen
	MintSlug SlugGen
}

// NewService wires a Service.
//
// Why an Options bag rather than positional args: the four
// dependencies (Reader, Mutator, Clock, IDGen, SlugGen) are easy
// to mix up in a positional call site, and most callers want the
// production defaults. The bag makes the test/non-test path
// symmetric.
func NewService(r Reader, m Mutator, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.MintID == nil {
		opts.MintID = defaultIDGen
	}
	if opts.MintSlug == nil {
		opts.MintSlug = defaultSlugGen
	}
	return &Service{r: r, m: m, now: opts.Now, mintID: opts.MintID, mintSlug: opts.MintSlug}
}

// Create validates the request, mints an ID, and persists.
func (s *Service) Create(ctx context.Context, tenantID, ownerUserID string, req CreateRequest) (Workspace, error) {
	if err := ValidateName(req.Name); err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
	}
	if err := ValidateDescription(req.Description); err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
	}
	if err := ValidateViews(req.Views); err != nil {
		return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
	}
	views := req.Views
	if views == nil {
		views = []View{}
	}
	w := Workspace{
		ID:          s.mintID(),
		TenantID:    tenantID,
		OwnerUserID: ownerUserID,
		Name:        req.Name,
		Description: req.Description,
		Views:       views,
	}
	return s.m.Insert(ctx, w)
}

// Get is a thin pass-through that exists for API symmetry — every
// service-layer access point goes through Service.
func (s *Service) Get(ctx context.Context, tenantID, id string) (Workspace, error) {
	return s.r.Get(ctx, tenantID, id)
}

// List passes through to the store.
func (s *Service) List(ctx context.Context, tenantID string) ([]Workspace, error) {
	return s.r.List(ctx, tenantID)
}

// Update validates the request and writes through. Empty patch
// (every field nil) is a no-op that still bumps updated_at — this
// gives operators a way to "touch" a workspace.
func (s *Service) Update(ctx context.Context, tenantID, id string, req UpdateRequest) (Workspace, error) {
	if req.Name != nil {
		if err := ValidateName(*req.Name); err != nil {
			return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
		}
	}
	if req.Description != nil {
		if err := ValidateDescription(*req.Description); err != nil {
			return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
		}
	}
	if req.Views != nil {
		if err := ValidateViews(*req.Views); err != nil {
			return Workspace{}, fmt.Errorf("%w: %v", ErrInvalidWorkspace, err)
		}
	}
	return s.m.Update(ctx, tenantID, id, req)
}

// Delete passes through.
func (s *Service) Delete(ctx context.Context, tenantID, id string) error {
	return s.m.Delete(ctx, tenantID, id)
}

// Share mints a fresh slug and sets the expiry. Idempotent in the
// sense that calling Share twice gives a fresh slug each time —
// the previous slug is effectively revoked when overwritten.
//
// Why mint a fresh slug on each call rather than reuse the
// existing one: an operator who Shares a workspace twice probably
// wants a new link (the original might have leaked). If they
// want a stable slug, they don't call Share repeatedly.
func (s *Service) Share(ctx context.Context, tenantID, id string, opts ShareOptions) (Workspace, error) {
	ttl := opts.TTL
	if ttl < 0 {
		return Workspace{}, fmt.Errorf("%w: ttl must be non-negative", ErrInvalidWorkspace)
	}
	if ttl == 0 {
		ttl = DefaultShareTTL
	}
	slug, err := s.mintSlug()
	if err != nil {
		return Workspace{}, fmt.Errorf("workspaces: mint slug: %w", err)
	}
	expires := s.now().Add(ttl)
	return s.m.SetShare(ctx, tenantID, id, slug, &expires)
}

// Unshare clears the slug + expiry. Idempotent — calling Unshare
// on an already-unshared workspace is a no-op (the row is updated
// with the same NULLs and updated_at moves forward).
func (s *Service) Unshare(ctx context.Context, tenantID, id string) (Workspace, error) {
	return s.m.SetShare(ctx, tenantID, id, "", nil)
}

// Resolve returns a workspace by share slug. ErrShareNotFound
// when the slug is unknown or expired.
func (s *Service) Resolve(ctx context.Context, slug string) (Workspace, error) {
	return s.r.GetByShareSlug(ctx, slug, s.now())
}

// defaultIDGen mints `wks-<8 hex chars>` from crypto/rand.
func defaultIDGen() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Reads from crypto/rand never fail in normal operation;
		// when they do (some FIPS contexts), the right answer is
		// to crash than to silently use a degenerate ID.
		panic(fmt.Errorf("workspaces: crypto/rand: %w", err))
	}
	return fmt.Sprintf("wks-%x", buf)
}

// defaultSlugGen produces a 16-byte base64url-encoded slug — 22
// URL-safe characters, ~128 bits of entropy. Unguessable to the
// extent crypto/rand is.
func defaultSlugGen() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// ensure Reader/Mutator interfaces are satisfied by *Store at
// compile time.
var (
	_ Reader  = (*Store)(nil)
	_ Mutator = (*Store)(nil)
)

// _ avoids "imported and not used" if a future change drops
// errors. Currently used in the file — the import stays clean.
var _ = errors.New
