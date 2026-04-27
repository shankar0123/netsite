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

// What: integration tests for the ClickHouse schema applier. They spin
// up a real ClickHouse 24 container via testcontainers-go, apply the
// embedded schema set, and assert end-state plus idempotency.
//
// How: gated behind the `integration` build tag so unit-only test runs
// (`go test ./...`) skip them. Invoked by `make test-integration`.
//
// Why a real ClickHouse rather than a mock: the applier's correctness
// depends on ClickHouse-specific semantics (ReplacingMergeTree merge
// behavior, FINAL modifier dedup at read time, native protocol single-
// statement Exec). A mock would require us to reimplement those rules
// in test glue and would silently diverge from production over time.

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startClickHouse returns a running ClickHouse 24 container and a DSN
// that reaches it. Cleanup is registered via t.Cleanup so individual
// test cases need not handle teardown.
func startClickHouse(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:24-alpine",
		tcclickhouse.WithUsername("netsite"),
		tcclickhouse.WithPassword("netsite"),
		tcclickhouse.WithDatabase("netsite_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready for connections").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start clickhouse: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("clickhouse terminate: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

// TestApply_FreshDatabase_Applies asserts the applier runs against an
// empty database and that 0001_init.sql's effect is visible afterward.
func TestApply_FreshDatabase_Applies(t *testing.T) {
	dsn := startClickHouse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := Apply(ctx, conn, Schema()); err != nil {
		t.Fatalf("Apply (first): %v", err)
	}

	// 0001 created _ch_schema_smoke. Even with zero inserts the table
	// must exist; the applier itself does not insert into it.
	row := conn.QueryRow(ctx, `SELECT count(*) FROM system.tables WHERE database = currentDatabase() AND name = '_ch_schema_smoke'`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n != 1 {
		t.Errorf("system.tables shows %d rows for _ch_schema_smoke; want 1", n)
	}

	// _ch_schema_applied records the file name (use FINAL to merge
	// in-flight ReplacingMergeTree duplicates).
	row = conn.QueryRow(ctx, `SELECT count(*) FROM _ch_schema_applied FINAL WHERE name = '0001_init.sql'`)
	var applied uint64
	if err := row.Scan(&applied); err != nil {
		t.Fatalf("read tracking: %v", err)
	}
	if applied != 1 {
		t.Errorf("_ch_schema_applied for 0001_init.sql = %d; want 1", applied)
	}
}

// TestApply_SecondApply_IsNoOp asserts the applier is idempotent: a
// second Apply() call against the same database changes nothing
// observable.
func TestApply_SecondApply_IsNoOp(t *testing.T) {
	dsn := startClickHouse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := Apply(ctx, conn, Schema()); err != nil {
		t.Fatalf("Apply first: %v", err)
	}

	// Second apply.
	if err := Apply(ctx, conn, Schema()); err != nil {
		t.Fatalf("Apply second: %v", err)
	}

	// Tracking shows a single row per name when read with FINAL.
	row := conn.QueryRow(ctx, `SELECT count(*) FROM _ch_schema_applied FINAL`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count tracking: %v", err)
	}
	if n != 1 {
		t.Errorf("_ch_schema_applied count after double apply = %d; want 1", n)
	}
}

// TestApply_NilSrc asserts the applier refuses a nil src with a clear
// error. This branch needs a real driver.Conn to reach (because the
// nil-conn guard runs first), so it lives in the integration suite.
func TestApply_NilSrc(t *testing.T) {
	dsn := startClickHouse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := Apply(ctx, conn, nil); err == nil {
		t.Fatal("Apply with nil src returned nil error")
	}
}
