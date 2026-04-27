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

package annotations

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
		t.Fatalf("start postgres: %v", err)
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
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.Migrate(ctx, pool, postgres.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func seedTenantAndUser(t *testing.T, pool *pgxpool.Pool, tenantID, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		tenantID, "test"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, password_hash, role)
         VALUES ($1, $2, $3, $4, 'admin') ON CONFLICT DO NOTHING`,
		userID, tenantID, "u@example.com", "fakehash"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestStore_Roundtrip exercises Insert → Get → List → Delete with
// a real Postgres + the migration set including 0007_annotations.
func TestStore_Roundtrip(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndUser(t, pool, "tnt-int", "usr-int")
	store := NewStore(pool)
	ctx := context.Background()

	at := time.Date(2026, 4, 27, 12, 30, 15, 0, time.UTC)
	a, err := store.Insert(ctx, Annotation{
		ID:       "ann-int-001",
		TenantID: "tnt-int",
		Scope:    ScopeCanary,
		ScopeID:  "tst-foo",
		At:       at,
		BodyMD:   "rolled forward",
		AuthorID: "usr-int",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if a.CreatedAt.IsZero() {
		t.Errorf("CreatedAt unset")
	}

	got, err := store.Get(ctx, "tnt-int", "ann-int-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BodyMD != "rolled forward" {
		t.Errorf("body round-trip lost")
	}

	// List with each filter dimension.
	rows, err := store.List(ctx, "tnt-int", ListFilter{Scope: ScopeCanary, ScopeID: "tst-foo"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("len(List) = %d; want 1", len(rows))
	}

	from := at.Add(-time.Minute)
	to := at.Add(time.Minute)
	rows, err = store.List(ctx, "tnt-int", ListFilter{From: &from, To: &to})
	if err != nil {
		t.Fatalf("List(time bounds): %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("time-bounded list returned %d; want 1", len(rows))
	}

	if err := store.Delete(ctx, "tnt-int", "ann-int-001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "tnt-int", "ann-int-001"); !errors.Is(err, ErrAnnotationNotFound) {
		t.Errorf("Get after Delete: err = %v; want ErrAnnotationNotFound", err)
	}
}

// TestStore_TenantIsolation asserts cross-tenant reads miss.
func TestStore_TenantIsolation(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndUser(t, pool, "tnt-A", "usr-A")
	seedTenantAndUser(t, pool, "tnt-B", "usr-B")
	store := NewStore(pool)
	ctx := context.Background()

	if _, err := store.Insert(ctx, Annotation{
		ID: "ann-iso", TenantID: "tnt-A", Scope: ScopeCanary, ScopeID: "tst-x",
		At: time.Now().UTC(), AuthorID: "usr-A",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Get(ctx, "tnt-B", "ann-iso"); !errors.Is(err, ErrAnnotationNotFound) {
		t.Errorf("cross-tenant Get returned %v; want ErrAnnotationNotFound", err)
	}
	rows, _ := store.List(ctx, "tnt-B", ListFilter{})
	if len(rows) != 0 {
		t.Errorf("cross-tenant List returned %v", rows)
	}
}

// TestStore_BadScopeRejectedByCheck asserts the SQL CHECK constraint
// is the second line of defence — even if the service-layer validator
// were bypassed, the database refuses bad scope values.
func TestStore_BadScopeRejectedByCheck(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndUser(t, pool, "tnt-int", "usr-int")
	store := NewStore(pool)
	ctx := context.Background()

	_, err := store.Insert(ctx, Annotation{
		ID: "ann-bad", TenantID: "tnt-int", Scope: "banana", ScopeID: "x",
		At: time.Now().UTC(), AuthorID: "usr-int",
	})
	if err == nil {
		t.Fatal("expected CHECK constraint failure on bad scope")
	}
}
