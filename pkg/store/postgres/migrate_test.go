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

// What: integration tests for the migration runner. They spin up a real
// Postgres 16 container via testcontainers-go, apply the embedded
// migration set, and assert end-state plus idempotency.
//
// How: gated behind the `integration` build tag so unit-only test runs
// (`go test ./...`) skip them. Invoked by `make test-integration`. CI
// runs them on every push because GitHub Actions ships with Docker.
//
// Why a real Postgres rather than a mock: the runner's correctness
// depends on Postgres-specific transactional DDL semantics (BEGIN around
// CREATE TABLE, ON CONFLICT, TIMESTAMPTZ defaults). A mock would require
// us to re-encode that behavior in test glue and would diverge from
// production over time. Containers are cheap; correctness is not.

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startPostgres returns a running Postgres 16 container and a DSN that
// reaches it. The container is registered for cleanup via t.Cleanup so
// individual test cases need not handle teardown.
func startPostgres(t *testing.T) (dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("netsite_test"),
		tcpostgres.WithUsername("netsite"),
		tcpostgres.WithPassword("netsite"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(45*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		// Use a fresh context so cleanup runs even if the test context
		// has been canceled.
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("postgres terminate: %v", err)
		}
	})

	dsn, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

// TestMigrate_FreshDatabase_Applies asserts the runner applies the
// embedded migration set against an empty database and the
// 0001_init.sql migration's effect is visible afterward.
func TestMigrate_FreshDatabase_Applies(t *testing.T) {
	dsn := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool, Migrations()); err != nil {
		t.Fatalf("Migrate (first apply): %v", err)
	}

	// The 0001 migration creates _migrations_smoke and inserts one row.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _migrations_smoke`).Scan(&n); err != nil {
		t.Fatalf("count smoke rows: %v", err)
	}
	if n != 1 {
		t.Errorf("_migrations_smoke row count = %d; want 1", n)
	}

	// _schema_migrations records the file name.
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM _schema_migrations ORDER BY name LIMIT 1`).Scan(&name); err != nil {
		t.Fatalf("read tracking: %v", err)
	}
	if name != "0001_init.sql" {
		t.Errorf("tracking name = %q; want %q", name, "0001_init.sql")
	}
}

// TestMigrate_SecondApply_IsNoOp asserts the runner is idempotent: a
// second Migrate() call against the same database changes nothing.
// This is the property CI re-runs depend on, and the property a fresh
// Helm install depends on if it accidentally re-runs the migration job.
func TestMigrate_SecondApply_IsNoOp(t *testing.T) {
	dsn := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool, Migrations()); err != nil {
		t.Fatalf("Migrate first: %v", err)
	}

	var rowCountBefore, trackingCountBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _migrations_smoke`).Scan(&rowCountBefore); err != nil {
		t.Fatalf("count rows before: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _schema_migrations`).Scan(&trackingCountBefore); err != nil {
		t.Fatalf("count tracking before: %v", err)
	}

	// Second apply.
	if err := Migrate(ctx, pool, Migrations()); err != nil {
		t.Fatalf("Migrate second: %v", err)
	}

	var rowCountAfter, trackingCountAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _migrations_smoke`).Scan(&rowCountAfter); err != nil {
		t.Fatalf("count rows after: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _schema_migrations`).Scan(&trackingCountAfter); err != nil {
		t.Fatalf("count tracking after: %v", err)
	}

	if rowCountAfter != rowCountBefore {
		t.Errorf("smoke row count changed: before=%d after=%d", rowCountBefore, rowCountAfter)
	}
	if trackingCountAfter != trackingCountBefore {
		t.Errorf("tracking row count changed: before=%d after=%d", trackingCountBefore, trackingCountAfter)
	}
}

// TestMigrate_NilSrc asserts the runner refuses a nil src with a clear
// error. This branch needs a real *pgxpool.Pool to reach (because the
// nil-pool guard runs first), so it lives in the integration suite.
func TestMigrate_NilSrc(t *testing.T) {
	dsn := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool, nil); err == nil {
		t.Fatal("Migrate with nil src returned nil error")
	}
}

// TestMigrate_IdempotentEvenWithoutTracking asserts the SQL itself is
// idempotent: if an operator manually truncates _schema_migrations, the
// runner will re-apply 0001_init.sql and still arrive at the right end
// state. This is the "defense in depth" property documented in the
// migrations/README.md.
func TestMigrate_IdempotentEvenWithoutTracking(t *testing.T) {
	dsn := startPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool, Migrations()); err != nil {
		t.Fatalf("Migrate first: %v", err)
	}

	// Truncate tracking table to simulate operator error.
	if _, err := pool.Exec(ctx, `TRUNCATE _schema_migrations`); err != nil {
		t.Fatalf("truncate tracking: %v", err)
	}

	// Re-apply. This now re-runs the 0001 SQL because the runner does
	// not know it has been applied. The SQL itself must be a no-op.
	if err := Migrate(ctx, pool, Migrations()); err != nil {
		t.Fatalf("Migrate second (after truncate): %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM _migrations_smoke`).Scan(&n); err != nil {
		t.Fatalf("count smoke rows: %v", err)
	}
	if n != 1 {
		t.Errorf("after truncate + re-apply, smoke rows = %d; want 1", n)
	}
}
