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

//go:build integration

package workspaces

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/shankar0123/netsite/pkg/store/postgres"
)

// startPostgres spins up a fresh Postgres 16 container, applies the
// repo's migrations through the auth_core layer plus 0006_workspaces,
// and returns a connected pool. Lifetime cleanup is registered via
// t.Cleanup.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("netsite_test"),
		tcpostgres.WithUsername("netsite"),
		tcpostgres.WithPassword("netsite"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(ctx)
	})
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}
	pool, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}

// seedTenantAndUser inserts a tenant + user that the workspaces FKs
// can reference. We use raw inserts here rather than the auth.Store
// to keep this test self-contained.
func seedTenantAndUser(t *testing.T, pool *pgxpool.Pool, tenantID, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, name) VALUES ($1, $2)
         ON CONFLICT (id) DO NOTHING`,
		tenantID, "test"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, role)
         VALUES ($1, $2, $3, $4, 'admin')
         ON CONFLICT (id) DO NOTHING`,
		userID, tenantID, "u@example.com", "fakehash"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestStore_Roundtrip walks Insert → Get → List → Update →
// SetShare → GetByShareSlug → Delete in one happy-path flow against
// real Postgres.
func TestStore_Roundtrip(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndUser(t, pool, "tnt-int", "usr-int")
	store := NewStore(pool)
	ctx := context.Background()

	w, err := store.Insert(ctx, Workspace{
		ID:          "wks-int-001",
		TenantID:    "tnt-int",
		OwnerUserID: "usr-int",
		Name:        "integration",
		Description: "round-trip",
		Views:       []View{{Name: "v1", URL: "/x"}},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		t.Errorf("timestamps not populated: %+v", w)
	}

	got, err := store.Get(ctx, "tnt-int", "wks-int-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "integration" || len(got.Views) != 1 {
		t.Errorf("Get returned %+v", got)
	}

	all, err := store.List(ctx, "tnt-int")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("len(List) = %d; want 1", len(all))
	}

	newName := "renamed"
	upd, err := store.Update(ctx, "tnt-int", "wks-int-001", UpdateRequest{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != "renamed" {
		t.Errorf("Update name = %q", upd.Name)
	}
	if !upd.UpdatedAt.After(w.UpdatedAt) {
		t.Errorf("UpdatedAt not advanced: %v vs %v", upd.UpdatedAt, w.UpdatedAt)
	}

	expires := time.Now().UTC().Add(time.Hour)
	shared, err := store.SetShare(ctx, "tnt-int", "wks-int-001", "share-int-001", &expires)
	if err != nil {
		t.Fatalf("SetShare: %v", err)
	}
	if shared.ShareSlug != "share-int-001" {
		t.Errorf("ShareSlug = %q", shared.ShareSlug)
	}
	resolved, err := store.GetByShareSlug(ctx, "share-int-001", time.Now().UTC())
	if err != nil {
		t.Fatalf("GetByShareSlug: %v", err)
	}
	if resolved.ID != "wks-int-001" {
		t.Errorf("Resolve returned %s", resolved.ID)
	}

	// Expired slug → ErrShareNotFound (the SQL filters expired rows).
	if _, err := store.GetByShareSlug(ctx, "share-int-001", time.Now().UTC().Add(2*time.Hour)); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("expired Resolve: err = %v; want ErrShareNotFound", err)
	}

	if err := store.Delete(ctx, "tnt-int", "wks-int-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "tnt-int", "wks-int-001"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Errorf("Get after Delete: err = %v; want ErrWorkspaceNotFound", err)
	}
}

// TestStore_TenantIsolation asserts a workspace in tenant A is
// invisible from tenant B.
func TestStore_TenantIsolation(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndUser(t, pool, "tnt-A", "usr-A")
	seedTenantAndUser(t, pool, "tnt-B", "usr-B")
	store := NewStore(pool)
	ctx := context.Background()

	if _, err := store.Insert(ctx, Workspace{
		ID: "wks-iso", TenantID: "tnt-A", OwnerUserID: "usr-A", Name: "secret",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Get(ctx, "tnt-B", "wks-iso"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Errorf("cross-tenant Get: err = %v; want ErrWorkspaceNotFound", err)
	}
	rows, err := store.List(ctx, "tnt-B")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("cross-tenant List returned %v", rows)
	}
}
