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
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// memStore is an in-memory implementation of Reader+Mutator for
// service-layer tests. The whole point is to exercise the Service's
// validation and timestamp logic without touching Postgres.
type memStore struct {
	rows  map[string]Workspace // keyed by id
	slugs map[string]string    // share_slug → id
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]Workspace{}, slugs: map[string]string{}}
}

func (m *memStore) Insert(_ context.Context, w Workspace) (Workspace, error) {
	if w.Views == nil {
		w.Views = []View{}
	}
	w.CreatedAt = fixedNow
	w.UpdatedAt = fixedNow
	m.rows[w.ID] = w
	return w, nil
}

func (m *memStore) Get(_ context.Context, tenantID, id string) (Workspace, error) {
	w, ok := m.rows[id]
	if !ok || w.TenantID != tenantID {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return w, nil
}

func (m *memStore) List(_ context.Context, tenantID string) ([]Workspace, error) {
	out := []Workspace{}
	for _, w := range m.rows {
		if w.TenantID == tenantID {
			out = append(out, w)
		}
	}
	return out, nil
}

func (m *memStore) Update(_ context.Context, tenantID, id string, req UpdateRequest) (Workspace, error) {
	w, ok := m.rows[id]
	if !ok || w.TenantID != tenantID {
		return Workspace{}, ErrWorkspaceNotFound
	}
	if req.Name != nil {
		w.Name = *req.Name
	}
	if req.Description != nil {
		w.Description = *req.Description
	}
	if req.Views != nil {
		w.Views = *req.Views
	}
	w.UpdatedAt = fixedNow.Add(time.Hour)
	m.rows[id] = w
	return w, nil
}

func (m *memStore) Delete(_ context.Context, tenantID, id string) error {
	w, ok := m.rows[id]
	if !ok || w.TenantID != tenantID {
		return ErrWorkspaceNotFound
	}
	if w.ShareSlug != "" {
		delete(m.slugs, w.ShareSlug)
	}
	delete(m.rows, id)
	return nil
}

func (m *memStore) SetShare(_ context.Context, tenantID, id string, slug string, expires *time.Time) (Workspace, error) {
	w, ok := m.rows[id]
	if !ok || w.TenantID != tenantID {
		return Workspace{}, ErrWorkspaceNotFound
	}
	if w.ShareSlug != "" {
		delete(m.slugs, w.ShareSlug)
	}
	w.ShareSlug = slug
	w.ShareExpiresAt = expires
	w.UpdatedAt = fixedNow.Add(time.Hour)
	m.rows[id] = w
	if slug != "" {
		m.slugs[slug] = id
	}
	return w, nil
}

func (m *memStore) GetByShareSlug(_ context.Context, slug string, now time.Time) (Workspace, error) {
	id, ok := m.slugs[slug]
	if !ok {
		return Workspace{}, ErrShareNotFound
	}
	w := m.rows[id]
	if w.ShareExpiresAt != nil && !w.ShareExpiresAt.After(now) {
		return Workspace{}, ErrShareNotFound
	}
	return w, nil
}

// fixedNow is the deterministic clock used throughout the test.
var fixedNow = time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

// newServiceWithFakes returns a Service wired against the in-memory
// store, with deterministic ID + slug generators so test
// assertions are stable.
func newServiceWithFakes(t *testing.T) (*Service, *memStore) {
	t.Helper()
	store := newMemStore()
	idCount := 0
	idGen := func() string {
		idCount++
		return strings.Repeat("z", 0) + "wks-test-" + idstr(idCount)
	}
	slugCount := 0
	slugGen := func() (string, error) {
		slugCount++
		return "slug-" + idstr(slugCount), nil
	}
	svc := NewService(store, store, Options{
		Now:      func() time.Time { return fixedNow },
		MintID:   idGen,
		MintSlug: slugGen,
	})
	return svc, store
}

// idstr is just strconv.Itoa under a shorter name to keep test
// closures concise.
func idstr(n int) string { return strconv.Itoa(n) }

// TestService_CreateValidates rejects bad input.
func TestService_CreateValidates(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	cases := []struct {
		name string
		req  CreateRequest
	}{
		{"empty name", CreateRequest{Name: ""}},
		{"too long name", CreateRequest{Name: strings.Repeat("x", MaxNameLen+1)}},
		{"too long description", CreateRequest{Name: "ok", Description: strings.Repeat("x", MaxDescriptionLen+1)}},
		{"too many views", CreateRequest{Name: "ok", Views: makeViews(MaxViews + 1)}},
		{"view missing name", CreateRequest{Name: "ok", Views: []View{{URL: "/"}}}},
		{"view missing url", CreateRequest{Name: "ok", Views: []View{{Name: "x"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, "tnt-X", "usr-Y", tc.req)
			if !errors.Is(err, ErrInvalidWorkspace) {
				t.Errorf("err = %v; want ErrInvalidWorkspace", err)
			}
		})
	}
}

// TestService_CreateAndGet round-trips a workspace.
func TestService_CreateAndGet(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, err := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
		Name: "Q3 incident review",
		Views: []View{
			{Name: "p95 latency", URL: "/dashboard/canary?metric=latency_p95"},
			{Name: "errors by pop", URL: "/dashboard/canary?metric=count&groupby=pop"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(w.ID, "wks-test-") {
		t.Errorf("ID = %q; want wks-test- prefix", w.ID)
	}
	if w.TenantID != "tnt-X" || w.OwnerUserID != "usr-Y" {
		t.Errorf("scope mismatch: %+v", w)
	}
	got, err := svc.Get(ctx, "tnt-X", w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != w.Name || len(got.Views) != 2 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Cross-tenant Get must miss.
	if _, err := svc.Get(ctx, "tnt-OTHER", w.ID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Errorf("cross-tenant Get returned %v; want ErrWorkspaceNotFound", err)
	}
}

// TestService_Update applies a partial patch.
func TestService_Update(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "first"})
	newName := "renamed"
	got, err := svc.Update(ctx, "tnt-X", w.ID, UpdateRequest{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q; want renamed", got.Name)
	}
	// Validation runs on UpdateRequest.
	bad := strings.Repeat("x", MaxNameLen+1)
	if _, err := svc.Update(ctx, "tnt-X", w.ID, UpdateRequest{Name: &bad}); !errors.Is(err, ErrInvalidWorkspace) {
		t.Errorf("Update with too-long name: err = %v; want ErrInvalidWorkspace", err)
	}
}

// TestService_ShareAndResolve mints a slug and resolves it back.
func TestService_ShareAndResolve(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "shareable"})
	shared, err := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if shared.ShareSlug == "" {
		t.Error("expected non-empty slug")
	}
	if shared.ShareExpiresAt == nil || !shared.ShareExpiresAt.Equal(fixedNow.Add(DefaultShareTTL)) {
		t.Errorf("expiry = %v; want %v", shared.ShareExpiresAt, fixedNow.Add(DefaultShareTTL))
	}
	got, err := svc.Resolve(ctx, shared.ShareSlug)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != w.ID {
		t.Errorf("Resolve returned %s; want %s", got.ID, w.ID)
	}
	// Unknown slug → ErrShareNotFound.
	if _, err := svc.Resolve(ctx, "no-such-slug"); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("unknown slug: err = %v; want ErrShareNotFound", err)
	}
}

// TestService_ShareCustomTTL respects an explicit TTL.
func TestService_ShareCustomTTL(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "n"})
	shared, err := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{TTL: 30 * time.Minute})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	want := fixedNow.Add(30 * time.Minute)
	if !shared.ShareExpiresAt.Equal(want) {
		t.Errorf("expiry = %v; want %v", shared.ShareExpiresAt, want)
	}
}

// TestService_ShareNegativeTTLRejected rejects bad TTL.
func TestService_ShareNegativeTTLRejected(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "n"})
	if _, err := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{TTL: -1}); !errors.Is(err, ErrInvalidWorkspace) {
		t.Errorf("err = %v; want ErrInvalidWorkspace", err)
	}
}

// TestService_Unshare clears the slug.
func TestService_Unshare(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "n"})
	shared, _ := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{})
	got, err := svc.Unshare(ctx, "tnt-X", w.ID)
	if err != nil {
		t.Fatalf("Unshare: %v", err)
	}
	if got.ShareSlug != "" || got.ShareExpiresAt != nil {
		t.Errorf("expected cleared share; got %+v", got)
	}
	// The previously-minted slug should no longer resolve.
	if _, err := svc.Resolve(ctx, shared.ShareSlug); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("Resolve after unshare: err = %v; want ErrShareNotFound", err)
	}
}

// TestService_ResolveExpired surfaces ErrShareNotFound when the
// link has aged out, even though the row still exists.
func TestService_ResolveExpired(t *testing.T) {
	store := newMemStore()
	// Use a clock we can advance.
	now := fixedNow
	clock := func() time.Time { return now }
	idGen := func() string { return "wks-test-1" }
	slugGen := func() (string, error) { return "abc", nil }
	svc := NewService(store, store, Options{Now: clock, MintID: idGen, MintSlug: slugGen})
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "n"})
	if _, err := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{TTL: time.Minute}); err != nil {
		t.Fatalf("Share: %v", err)
	}
	// Advance time past expiry.
	now = now.Add(2 * time.Minute)
	if _, err := svc.Resolve(ctx, "abc"); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("Resolve(expired): err = %v; want ErrShareNotFound", err)
	}
}

// TestService_DeleteRemoves removes the row and its slug.
func TestService_DeleteRemoves(t *testing.T) {
	svc, store := newServiceWithFakes(t)
	ctx := context.Background()
	w, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{Name: "n"})
	if _, err := svc.Share(ctx, "tnt-X", w.ID, ShareOptions{}); err != nil {
		t.Fatalf("Share: %v", err)
	}
	if err := svc.Delete(ctx, "tnt-X", w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, "tnt-X", w.ID); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Errorf("Get after Delete: err = %v; want ErrWorkspaceNotFound", err)
	}
	if len(store.slugs) != 0 {
		t.Errorf("expected slug map cleared; got %v", store.slugs)
	}
}

// TestService_List returns only the requesting tenant's rows.
func TestService_List(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "tnt-A", "usr-1", CreateRequest{Name: "a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "tnt-A", "usr-1", CreateRequest{Name: "a2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, "tnt-B", "usr-2", CreateRequest{Name: "b1"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.List(ctx, "tnt-A")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(List) = %d; want 2", len(got))
	}
	for _, w := range got {
		if w.TenantID != "tnt-A" {
			t.Errorf("cross-tenant leak: %+v", w)
		}
	}
}

// TestDefaultIDGen / TestDefaultSlugGen exercise the production
// generators directly so they don't sit at 0% coverage.
func TestDefaultIDGen(t *testing.T) {
	id := defaultIDGen()
	if !strings.HasPrefix(id, "wks-") || len(id) != 12 { // wks- + 8 hex chars
		t.Errorf("default ID = %q (len %d); want wks- + 8 hex chars", id, len(id))
	}
}

func TestDefaultSlugGen(t *testing.T) {
	s, err := defaultSlugGen()
	if err != nil {
		t.Fatalf("defaultSlugGen: %v", err)
	}
	if len(s) != 22 { // base64url of 16 bytes, no padding
		t.Errorf("default slug = %q (len %d); want 22", s, len(s))
	}
}

// makeViews returns n synthetic views for over-cap tests.
func makeViews(n int) []View {
	out := make([]View, n)
	for i := range out {
		out[i] = View{Name: "v", URL: "/x"}
	}
	return out
}
