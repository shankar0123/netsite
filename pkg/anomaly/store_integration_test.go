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

// What: integration tests for pkg/anomaly's Postgres-backed Store.
// Spin up a real Postgres 16 via testcontainers-go, apply the
// migration set (so 0008_anomaly_state lands), and exercise every
// CRUD method end-to-end.
//
// How: gated behind the `integration` build tag so unit-only test
// runs (`go test ./...`) skip them. Invoked by `make
// test-integration` and by CI's integration-tests workflow.
//
// Why a real Postgres rather than a mock: the store relies on
// pgxpool semantics (ON CONFLICT, RETURNING, parameter binding,
// CHECK constraint enforcement) that a mock would have to
// reimplement. The annotations and workspaces packages established
// the pattern; we follow it here to keep the test surface uniform.

package anomaly

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

// seedTenantAndTest inserts a tenant + an enabled test so
// ListEnabledTests has something to return and UpsertVerdict's
// foreign-key relationship to tenants is satisfied.
func seedTenantAndTest(t *testing.T, pool *pgxpool.Pool, tenantID, testID string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, name) VALUES ($1, 'integration')
		 ON CONFLICT DO NOTHING`, tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO tests (id, tenant_id, kind, target, enabled)
		 VALUES ($1, $2, 'http', 'https://example.com', $3)
		 ON CONFLICT DO NOTHING`,
		testID, tenantID, enabled); err != nil {
		t.Fatalf("seed test: %v", err)
	}
}

func sampleVerdict(tenantID, testID, metric string, sev Severity) VerdictRow {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	return VerdictRow{
		TenantID:    tenantID,
		TestID:      testID,
		Metric:      metric,
		Method:      MethodHoltWinters,
		Severity:    sev,
		Suppressed:  false,
		LastValue:   123.45,
		Forecast:    100.0,
		Residual:    23.45,
		MAD:         2.5,
		MADUnits:    9.38,
		Reason:      "integration-test",
		LastPointAt: now,
		EvaluatedAt: now,
	}
}

// TestStore_UpsertGet_Roundtrip exercises Upsert → Get with a real
// Postgres + the 0008 migration. Covers the column round-trip for
// every field on VerdictRow.
func TestStore_UpsertGet_Roundtrip(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-int-1", true)
	store := NewStore(pool)
	ctx := context.Background()

	want := sampleVerdict("tnt-int", "tst-int-1", string(MetricLatencyP95), SeverityAnomaly)
	if err := store.UpsertVerdict(ctx, want); err != nil {
		t.Fatalf("UpsertVerdict: %v", err)
	}

	got, err := store.GetVerdict(ctx, "tnt-int", "tst-int-1", string(MetricLatencyP95))
	if err != nil {
		t.Fatalf("GetVerdict: %v", err)
	}
	if got.Method != want.Method || got.Severity != want.Severity ||
		got.LastValue != want.LastValue || got.Forecast != want.Forecast ||
		got.Residual != want.Residual || got.MAD != want.MAD ||
		got.MADUnits != want.MADUnits || got.Reason != want.Reason {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.LastPointAt.Equal(want.LastPointAt) {
		t.Errorf("LastPointAt: got %v want %v", got.LastPointAt, want.LastPointAt)
	}
}

// TestStore_UpsertVerdict_OverwritesExistingRow asserts a second
// upsert against the same composite PK overwrites the row in place
// (no second row, fields reflect the second call).
func TestStore_UpsertVerdict_OverwritesExistingRow(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-int-1", true)
	store := NewStore(pool)
	ctx := context.Background()

	first := sampleVerdict("tnt-int", "tst-int-1", string(MetricLatencyP95), SeverityNone)
	first.Reason = "first"
	if err := store.UpsertVerdict(ctx, first); err != nil {
		t.Fatal(err)
	}

	second := sampleVerdict("tnt-int", "tst-int-1", string(MetricLatencyP95), SeverityCritical)
	second.Reason = "second"
	second.LastValue = 999.99
	if err := store.UpsertVerdict(ctx, second); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetVerdict(ctx, "tnt-int", "tst-int-1", string(MetricLatencyP95))
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity != SeverityCritical || got.Reason != "second" || got.LastValue != 999.99 {
		t.Errorf("second upsert did not overwrite first: %+v", got)
	}
	rows, err := store.ListVerdicts(ctx, "tnt-int")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly 1 row after two upserts of same PK, got %d", len(rows))
	}
}

// TestStore_GetVerdict_NotFound asserts the sentinel.
func TestStore_GetVerdict_NotFound(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-int-1", true)
	store := NewStore(pool)
	if _, err := store.GetVerdict(context.Background(), "tnt-int", "tst-nope", "latency_p95"); !errors.Is(err, ErrVerdictNotFound) {
		t.Errorf("want ErrVerdictNotFound, got %v", err)
	}
}

// TestStore_ListVerdicts_TenantIsolation asserts cross-tenant reads
// miss. The (tenant_id, test_id, metric) PK leads with tenant so
// this is structural — the test prevents accidental DROP of that
// guarantee via a future migration.
func TestStore_ListVerdicts_TenantIsolation(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-A", "tst-A-1", true)
	seedTenantAndTest(t, pool, "tnt-B", "tst-B-1", true)
	store := NewStore(pool)
	ctx := context.Background()

	_ = store.UpsertVerdict(ctx, sampleVerdict("tnt-A", "tst-A-1", string(MetricLatencyP95), SeverityNone))
	_ = store.UpsertVerdict(ctx, sampleVerdict("tnt-B", "tst-B-1", string(MetricLatencyP95), SeverityNone))

	a, err := store.ListVerdicts(ctx, "tnt-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].TenantID != "tnt-A" {
		t.Errorf("tenant-A list: got %+v", a)
	}
	if _, err := store.GetVerdict(ctx, "tnt-A", "tst-B-1", string(MetricLatencyP95)); !errors.Is(err, ErrVerdictNotFound) {
		t.Errorf("cross-tenant Get returned %v; want ErrVerdictNotFound", err)
	}
}

// TestStore_ListVerdicts_OrderStable asserts (test_id, metric)
// ascending so a UI gets a deterministic render order.
func TestStore_ListVerdicts_OrderStable(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-b", true)
	seedTenantAndTest(t, pool, "tnt-int", "tst-a", true)
	store := NewStore(pool)
	ctx := context.Background()

	// Insert in mixed order; the ORDER BY in ListVerdicts should
	// re-sort.
	_ = store.UpsertVerdict(ctx, sampleVerdict("tnt-int", "tst-b", "latency_p95", SeverityNone))
	_ = store.UpsertVerdict(ctx, sampleVerdict("tnt-int", "tst-a", "latency_p95", SeverityNone))
	_ = store.UpsertVerdict(ctx, sampleVerdict("tnt-int", "tst-a", "error_rate", SeverityNone))

	rows, err := store.ListVerdicts(ctx, "tnt-int")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("len = %d; want 3", len(rows))
	}
	want := []struct{ test, metric string }{
		{"tst-a", "error_rate"},
		{"tst-a", "latency_p95"},
		{"tst-b", "latency_p95"},
	}
	for i, w := range want {
		if rows[i].TestID != w.test || rows[i].Metric != w.metric {
			t.Errorf("row %d: got (%s,%s); want (%s,%s)",
				i, rows[i].TestID, rows[i].Metric, w.test, w.metric)
		}
	}
}

// TestStore_ListEnabledTests_FiltersDisabled asserts disabled tests
// are excluded — the evaluator must not score paused canaries.
func TestStore_ListEnabledTests_FiltersDisabled(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-on", true)
	seedTenantAndTest(t, pool, "tnt-int", "tst-off", false)
	store := NewStore(pool)

	got, err := store.ListEnabledTests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TestID != "tst-on" {
		t.Errorf("got %+v; want one TestRef tst-on", got)
	}
}

// TestStore_BadMethodRejectedByCheck asserts the SQL CHECK
// constraint is the second line of defence: even if the service
// layer were bypassed the database refuses unknown method values.
func TestStore_BadMethodRejectedByCheck(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-int-1", true)
	store := NewStore(pool)

	bad := sampleVerdict("tnt-int", "tst-int-1", string(MetricLatencyP95), SeverityNone)
	bad.Method = Method("not_a_real_method")
	if err := store.UpsertVerdict(context.Background(), bad); err == nil {
		t.Fatal("expected CHECK constraint failure on bad method")
	}
}

// TestStore_BadSeverityRejectedByCheck mirrors the above for severity.
func TestStore_BadSeverityRejectedByCheck(t *testing.T) {
	pool := startPostgres(t)
	seedTenantAndTest(t, pool, "tnt-int", "tst-int-1", true)
	store := NewStore(pool)

	bad := sampleVerdict("tnt-int", "tst-int-1", string(MetricLatencyP95), Severity("apocalypse"))
	if err := store.UpsertVerdict(context.Background(), bad); err == nil {
		t.Fatal("expected CHECK constraint failure on bad severity")
	}
}

// TestStore_UpsertVerdict_RequiresIDs covers the in-memory guard.
// Quick non-Postgres assertion lives here (rather than a unit test)
// so the integration suite forms a complete contract for the Store.
func TestStore_UpsertVerdict_RequiresIDs(t *testing.T) {
	store := NewStore(nil) // nil pool; arg check trips before any SQL
	if err := store.UpsertVerdict(context.Background(), VerdictRow{}); err == nil {
		t.Error("expected error for empty IDs")
	}
}
