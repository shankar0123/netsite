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

// Package workspaces is NetSite's saved-view bundle. An operator
// pins a few views (netql queries, dashboard routes, raw URLs) and
// gives the bundle a name; opening the workspace later returns the
// page exactly as they left it.
//
// What:
//   - Workspace: a tenant-scoped, owner-attributed bundle of Views
//     plus optional sharing metadata.
//   - View: one entry — a name, a URL/deep-link, and an optional
//     note (one or two sentences explaining what the view is for).
//   - Service: the small business-logic layer that validates input,
//     mints prefixed IDs and share slugs, and enforces tenant
//     scoping on every read.
//
// How: types.go declares the shapes and validation; store.go is a
// thin pgxpool-backed CRUD layer; service.go composes the two with
// a Clock abstraction so tests stay deterministic. Handlers in
// pkg/api/workspaces.go translate HTTP requests into Service calls.
//
// Why a single-purpose package rather than folding workspaces into
// pkg/api: the same shapes are reused by the share-link resolver
// (which has no auth and lives at /v1/share/{slug}). Putting the
// data model and business logic in one package keeps both surfaces
// honest and lets us add CLI / RPC bindings later without touching
// the model.
package workspaces

import (
	"errors"
	"strings"
	"time"
)

// View is one entry inside a Workspace.
//
// URL is the canonical deep-link the operator pinned (e.g.,
// `/v1/canaries?test=tst-foo`, or a netql query encoded as a query
// string). The frontend consumes the URL verbatim; we deliberately
// do not parse or normalise it here so the model stays loose for
// future deep-link shapes.
type View struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Note string `json:"note,omitempty"`
}

// Workspace is one saved bundle.
//
// ShareSlug carries a non-empty short ID exactly when the workspace
// has been made shareable; the store enforces the UNIQUE constraint
// at the SQL level so collisions surface as an error from Insert
// rather than overwriting an existing share.
//
// ShareExpiresAt may be set even when ShareSlug is empty — that's a
// no-op state that the service rejects on validation. Stored as a
// TIMESTAMPTZ so timezone math is unambiguous.
type Workspace struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	OwnerUserID    string     `json:"owner_user_id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Views          []View     `json:"views"`
	ShareSlug      string     `json:"share_slug,omitempty"`
	ShareExpiresAt *time.Time `json:"share_expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreateRequest is the input shape for Service.Create. Pulled out
// of Workspace so the handler doesn't accept caller-supplied IDs
// or timestamps.
type CreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Views       []View `json:"views"`
}

// UpdateRequest covers the mutable fields. Nil pointers mean
// "leave unchanged"; empty values explicitly clear the field
// (e.g., Description = pointer to empty string clears the
// description).
type UpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Views       *[]View `json:"views,omitempty"`
}

// ShareOptions tunes the share-slug minting in Service.Share. The
// zero value means "default share" — 7-day expiry, fresh slug.
//
// Why a struct rather than positional args: future options
// (recipient list, max-views-per-day rate limit, signed-with-tenant-
// CA) attach here without breaking the call site.
type ShareOptions struct {
	// TTL is the lifetime of the share. Defaults to DefaultShareTTL
	// when zero. Negative values are rejected by the service.
	TTL time.Duration
}

// Default constants.
const (
	// DefaultShareTTL is the default expiry for a share link when
	// the caller doesn't specify one. Seven days is the upper bound
	// most operators are comfortable with for "send this to a
	// colleague" links; anyone wanting longer can keep the
	// workspace tenant-internal and let the colleague log in.
	DefaultShareTTL = 7 * 24 * time.Hour

	// MaxNameLen / MaxDescriptionLen / MaxViewURLLen / MaxViews put
	// the obvious bounds on user-supplied content. The numbers are
	// generous — every real workspace fits well under these — but
	// they prevent a runaway client from posting a megabyte of
	// description text.
	MaxNameLen        = 200
	MaxDescriptionLen = 2000
	MaxViewURLLen     = 2048
	MaxViews          = 20
)

// Sentinel errors. All package errors that callers might want to
// distinguish flow through these.
var (
	ErrInvalidWorkspace  = errors.New("workspaces: invalid workspace")
	ErrWorkspaceNotFound = errors.New("workspaces: not found")
	ErrShareNotFound     = errors.New("workspaces: share not found or expired")
	ErrShareNotEnabled   = errors.New("workspaces: workspace is not shared")
)

// ValidateName enforces the workspace name's shape rules: non-blank
// after trimming, ≤ MaxNameLen bytes. Returns a non-nil error
// describing the violation; the service layer wraps it with
// ErrInvalidWorkspace.
//
// We split validation across small per-field functions rather than
// one big ValidateRequest so the same rules run on Create and the
// per-field paths in Update without duplicating the logic.
func ValidateName(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("name is required")
	}
	if len(s) > MaxNameLen {
		return errors.New("name exceeds max length")
	}
	return nil
}

// ValidateViews walks the View list and sanity-checks each entry.
func ValidateViews(views []View) error {
	if len(views) > MaxViews {
		return errors.New("too many views")
	}
	for i, v := range views {
		if strings.TrimSpace(v.Name) == "" {
			return errors.New("view name is required")
		}
		if strings.TrimSpace(v.URL) == "" {
			return errors.New("view url is required")
		}
		if len(v.URL) > MaxViewURLLen {
			return errors.New("view url exceeds max length")
		}
		_ = i
	}
	return nil
}

// ValidateDescription bounds the description string.
func ValidateDescription(s string) error {
	if len(s) > MaxDescriptionLen {
		return errors.New("description exceeds max length")
	}
	return nil
}
