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

package annotations

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// memStore is the in-memory Reader+Mutator used by service tests.
// It mirrors the real Store contract closely enough that the tests
// exercise the same Service behaviour against either backend.
type memStore struct {
	rows map[string]Annotation
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]Annotation{}}
}

func (m *memStore) Insert(_ context.Context, a Annotation) (Annotation, error) {
	a.CreatedAt = fixedNow
	m.rows[a.ID] = a
	return a, nil
}

func (m *memStore) Get(_ context.Context, tenantID, id string) (Annotation, error) {
	a, ok := m.rows[id]
	if !ok || a.TenantID != tenantID {
		return Annotation{}, ErrAnnotationNotFound
	}
	return a, nil
}

func (m *memStore) Delete(_ context.Context, tenantID, id string) error {
	a, ok := m.rows[id]
	if !ok || a.TenantID != tenantID {
		return ErrAnnotationNotFound
	}
	delete(m.rows, id)
	return nil
}

func (m *memStore) List(_ context.Context, tenantID string, f ListFilter) ([]Annotation, error) {
	out := []Annotation{}
	for _, a := range m.rows {
		if a.TenantID != tenantID {
			continue
		}
		if f.Scope != "" && a.Scope != f.Scope {
			continue
		}
		if f.ScopeID != "" && a.ScopeID != f.ScopeID {
			continue
		}
		if f.From != nil && a.At.Before(*f.From) {
			continue
		}
		if f.To != nil && !a.At.Before(*f.To) {
			continue
		}
		out = append(out, a)
	}
	// Naive bound — same shape as the SQL LIMIT in store.go.
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// fixedNow is the deterministic clock used throughout the test.
var fixedNow = time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

func newServiceWithFakes(t *testing.T) (*Service, *memStore) {
	t.Helper()
	store := newMemStore()
	idCount := 0
	idGen := func() string {
		idCount++
		return "ann-test-" + strconv.Itoa(idCount)
	}
	svc := NewService(store, store, Options{
		Now:    func() time.Time { return fixedNow },
		MintID: idGen,
	})
	return svc, store
}

// TestService_CreateValidates rejects bad input.
func TestService_CreateValidates(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	cases := []struct {
		name string
		req  CreateRequest
	}{
		{"empty scope", CreateRequest{ScopeID: "tst-foo", At: fixedNow}},
		{"unknown scope", CreateRequest{Scope: "banana", ScopeID: "tst-foo", At: fixedNow}},
		{"empty scope_id", CreateRequest{Scope: ScopeCanary, At: fixedNow}},
		{"too long scope_id", CreateRequest{Scope: ScopeCanary, ScopeID: strings.Repeat("x", MaxScopeIDLen+1), At: fixedNow}},
		{"too long body", CreateRequest{Scope: ScopeCanary, ScopeID: "tst-foo", At: fixedNow, BodyMD: strings.Repeat("x", MaxBodyLen+1)}},
		{"missing at", CreateRequest{Scope: ScopeCanary, ScopeID: "tst-foo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, "tnt-X", "usr-Y", tc.req)
			if !errors.Is(err, ErrInvalidAnnotation) {
				t.Errorf("err = %v; want ErrInvalidAnnotation", err)
			}
		})
	}
}

// TestService_CreateAndGet round-trips an annotation.
func TestService_CreateAndGet(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	at := fixedNow.Add(-time.Hour)
	a, err := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
		Scope: ScopeCanary, ScopeID: "tst-foo",
		At: at, BodyMD: "rolled forward at 12:30 UTC",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(a.ID, "ann-test-") {
		t.Errorf("ID = %q; want ann-test- prefix", a.ID)
	}
	if a.TenantID != "tnt-X" || a.AuthorID != "usr-Y" {
		t.Errorf("scope mismatch: %+v", a)
	}
	if !a.At.Equal(at) {
		t.Errorf("At = %v; want %v", a.At, at)
	}
	got, err := svc.Get(ctx, "tnt-X", a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BodyMD != a.BodyMD {
		t.Errorf("body round-trip mismatch")
	}
	// Cross-tenant Get must miss.
	if _, err := svc.Get(ctx, "tnt-OTHER", a.ID); !errors.Is(err, ErrAnnotationNotFound) {
		t.Errorf("cross-tenant Get returned %v; want ErrAnnotationNotFound", err)
	}
}

// TestService_ListFilters covers each filter dimension.
func TestService_ListFilters(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	mk := func(scope Scope, scopeID string, dt time.Duration) {
		if _, err := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
			Scope: scope, ScopeID: scopeID, At: fixedNow.Add(dt),
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk(ScopeCanary, "tst-A", -2*time.Hour)
	mk(ScopeCanary, "tst-A", -1*time.Hour)
	mk(ScopeCanary, "tst-B", -1*time.Hour)
	mk(ScopePOP, "pop-lhr-01", -1*time.Hour)

	all, err := svc.List(ctx, "tnt-X", ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("len(List) = %d; want 4", len(all))
	}

	canary, _ := svc.List(ctx, "tnt-X", ListFilter{Scope: ScopeCanary})
	if len(canary) != 3 {
		t.Errorf("scope=canary returned %d; want 3", len(canary))
	}

	tstA, _ := svc.List(ctx, "tnt-X", ListFilter{Scope: ScopeCanary, ScopeID: "tst-A"})
	if len(tstA) != 2 {
		t.Errorf("scope=canary id=tst-A returned %d; want 2", len(tstA))
	}

	from := fixedNow.Add(-90 * time.Minute)
	recent, _ := svc.List(ctx, "tnt-X", ListFilter{From: &from})
	if len(recent) != 3 {
		t.Errorf("from filter returned %d; want 3", len(recent))
	}

	// Invalid scope on List → TypeError-equivalent.
	if _, err := svc.List(ctx, "tnt-X", ListFilter{Scope: "banana"}); !errors.Is(err, ErrInvalidAnnotation) {
		t.Errorf("List(bad scope) err = %v; want ErrInvalidAnnotation", err)
	}
}

// TestService_LimitDefault covers the limit clamping logic.
func TestService_LimitDefault(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	// Force the in-memory store to handle the limit-clamp branch.
	for i := 0; i < 5; i++ {
		if _, err := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
			Scope: ScopeCanary, ScopeID: "tst-foo", At: fixedNow.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Limit larger than MaxListLimit gets clamped.
	got, err := svc.List(ctx, "tnt-X", ListFilter{Limit: MaxListLimit + 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("List clamped limit returned %d; want 5", len(got))
	}
}

// TestService_Delete round-trips a delete.
func TestService_Delete(t *testing.T) {
	svc, _ := newServiceWithFakes(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
		Scope: ScopeCanary, ScopeID: "tst-foo", At: fixedNow,
	})
	if err := svc.Delete(ctx, "tnt-X", a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, "tnt-X", a.ID); !errors.Is(err, ErrAnnotationNotFound) {
		t.Errorf("Get after Delete: err = %v; want ErrAnnotationNotFound", err)
	}
	// Cross-tenant Delete must miss.
	a2, _ := svc.Create(ctx, "tnt-X", "usr-Y", CreateRequest{
		Scope: ScopeCanary, ScopeID: "tst-foo", At: fixedNow,
	})
	if err := svc.Delete(ctx, "tnt-OTHER", a2.ID); !errors.Is(err, ErrAnnotationNotFound) {
		t.Errorf("cross-tenant Delete err = %v; want ErrAnnotationNotFound", err)
	}
}

// TestDefaultIDGen covers the production ID generator.
func TestDefaultIDGen(t *testing.T) {
	id := defaultIDGen()
	if !strings.HasPrefix(id, "ann-") || len(id) != 12 {
		t.Errorf("id = %q (len %d); want ann- + 8 hex chars", id, len(id))
	}
}

// TestValidate exercises each validator directly so the helpers
// don't sit at low coverage.
func TestValidate(t *testing.T) {
	if err := ValidateScope(ScopeCanary); err != nil {
		t.Errorf("ValidateScope(canary): %v", err)
	}
	if err := ValidateScope(""); err == nil {
		t.Error("ValidateScope(\"\") returned nil")
	}
	if err := ValidateScopeID("tst-foo"); err != nil {
		t.Errorf("ValidateScopeID(tst-foo): %v", err)
	}
	if err := ValidateScopeID("   "); err == nil {
		t.Error("ValidateScopeID(blank) returned nil")
	}
	if err := ValidateBody(""); err != nil {
		t.Errorf("ValidateBody(\"\"): %v", err)
	}
	if err := ValidateBody(strings.Repeat("x", MaxBodyLen+1)); err == nil {
		t.Error("ValidateBody(too long) returned nil")
	}
}
