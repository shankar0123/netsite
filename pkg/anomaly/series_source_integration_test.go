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

// What: integration tests for ClickHouseSeriesSource. Spin up a real
// ClickHouse 24, create the canary_results schema (the same one
// pkg/store/clickhouse/schema applies in production), insert
// hand-rolled rows, and assert the SeriesSource reads them back as
// the expected bucketed Series.
//
// Why a real ClickHouse rather than a mock: the source's correctness
// depends on quantile() / countIf() / toStartOfInterval() semantics
// and on the exact native-protocol parameter binding the Go driver
// does. Mocking would silently drift from production.

package anomaly

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	tcclickhouse "github.com/testcontainers/testcontainers-go/modules/clickhouse"

	chstore "github.com/shankar0123/netsite/pkg/store/clickhouse"
)

func startClickHouseConn(t *testing.T) driver.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := tcclickhouse.Run(ctx,
		"clickhouse/clickhouse-server:24-alpine",
		tcclickhouse.WithUsername("netsite"),
		tcclickhouse.WithPassword("netsite"),
		tcclickhouse.WithDatabase("netsite_test"),
	)
	if err != nil {
		t.Fatalf("start clickhouse: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dsn, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	conn, err := chstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("clickhouse Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Apply the canary_results schema inline. We could call
	// chstore.Apply(ctx, conn, chstore.Schema()) to get the
	// production migrations, but that runs three migrations and
	// pulls in the schema-applier's tracking table — overkill for
	// a SeriesSource test that only needs canary_results to exist.
	// Schema drift between this inline definition and the production
	// migrations is caught by pkg/canary/ingest's integration tests
	// and pkg/store/clickhouse's schema tests.
	if err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS canary_results (
			tenant_id     String,
			test_id       String,
			pop_id        String,
			observed_at   DateTime64(3, 'UTC'),
			latency_ms    Float32,
			dns_ms        Float32,
			connect_ms    Float32,
			tls_ms        Float32,
			ttfb_ms       Float32,
			status_code   UInt16,
			error_kind    LowCardinality(String),
			ja3           String,
			ja4           String,
			_inserted_at  DateTime DEFAULT now()
		)
		ENGINE = MergeTree
		PARTITION BY toYYYYMM(observed_at)
		ORDER BY (tenant_id, test_id, observed_at)
		SETTINGS index_granularity = 8192`); err != nil {
		t.Fatalf("create canary_results: %v", err)
	}
	return conn
}

// insertSampleRows writes one row per minute over the past 30 minutes
// for (tenant, test) with deterministic latency / error_kind values
// so SeriesSource.Series is testable.
func insertSampleRows(t *testing.T, conn driver.Conn, tenantID, testID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	batch, err := conn.PrepareBatch(ctx, `INSERT INTO canary_results (
		tenant_id, test_id, pop_id, observed_at,
		latency_ms, dns_ms, connect_ms, tls_ms, ttfb_ms,
		status_code, error_kind, ja3, ja4
	)`)
	if err != nil {
		t.Fatalf("PrepareBatch: %v", err)
	}
	for i := 0; i < 30; i++ {
		// Latency: 100 + i (so quantile(0.95) over a ten-minute
		// bucket is well-defined).
		latencyMS := float32(100 + i)
		// Errors: every 5th row is an error.
		errorKind := ""
		if i%5 == 0 {
			errorKind = "tls_handshake_failed"
		}
		ts := now.Add(-time.Duration(i) * time.Minute)
		if err := batch.Append(
			tenantID, testID, "pop-int-1", ts,
			latencyMS, float32(0), float32(0), float32(0), float32(0),
			uint16(200), errorKind, "", "",
		); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestClickHouseSeriesSource_LatencyP95(t *testing.T) {
	conn := startClickHouseConn(t)
	insertSampleRows(t, conn, "tnt-int", "tst-int-1")

	src := ClickHouseSeriesSource{Conn: conn, BucketDuration: 10 * time.Minute}
	series, err := src.Series(context.Background(),
		"tnt-int", "tst-int-1", MetricLatencyP95, time.Hour)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(series) == 0 {
		t.Fatal("expected at least one bucket; got empty series")
	}
	// All values in our injected data are 100..129 → p95 must fall
	// inside that range (with quantile interpolation, the upper
	// bucket can land slightly above 129; allow a small slack).
	for i, p := range series {
		if p.Value < 100 || p.Value > 130 {
			t.Errorf("bucket %d value %f outside [100,130]", i, p.Value)
		}
	}
}

func TestClickHouseSeriesSource_ErrorRate(t *testing.T) {
	conn := startClickHouseConn(t)
	insertSampleRows(t, conn, "tnt-int", "tst-int-1")

	src := ClickHouseSeriesSource{Conn: conn, BucketDuration: 10 * time.Minute}
	series, err := src.Series(context.Background(),
		"tnt-int", "tst-int-1", MetricErrorRate, time.Hour)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(series) == 0 {
		t.Fatal("expected at least one bucket; got empty series")
	}
	// Error rate is ~1/5 = 0.2 (deterministic by construction).
	// Per-bucket the rate may vary depending on which rows fall in
	// the bucket, but no bucket should exceed 1.0 or drop below 0.
	for i, p := range series {
		if p.Value < 0 || p.Value > 1 {
			t.Errorf("bucket %d error_rate %f outside [0,1]", i, p.Value)
		}
	}
}

// TestClickHouseSeriesSource_TenantIsolation_NoCrossRead asserts
// the WHERE tenant_id = ? clause is honoured. Inserts under tenant
// A and queries tenant B; the result must be empty.
func TestClickHouseSeriesSource_TenantIsolation_NoCrossRead(t *testing.T) {
	conn := startClickHouseConn(t)
	insertSampleRows(t, conn, "tnt-A", "tst-A-1")

	src := ClickHouseSeriesSource{Conn: conn}
	series, err := src.Series(context.Background(),
		"tnt-B", "tst-A-1", MetricLatencyP95, time.Hour)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("cross-tenant query returned %d points; want 0", len(series))
	}
}

// TestClickHouseSeriesSource_DefaultBucketIsHour asserts the
// "BucketDuration <= 0 → 1 hour" branch in Series() — no panic, and
// the call returns successfully (the result may be empty depending
// on the test's clock alignment, which is fine; we only need the
// branch to execute without erroring).
func TestClickHouseSeriesSource_DefaultBucketIsHour(t *testing.T) {
	conn := startClickHouseConn(t)
	insertSampleRows(t, conn, "tnt-int", "tst-int-1")

	src := ClickHouseSeriesSource{Conn: conn} // no BucketDuration → default
	if _, err := src.Series(context.Background(),
		"tnt-int", "tst-int-1", MetricLatencyP95, 24*time.Hour); err != nil {
		t.Fatalf("Series: %v", err)
	}
}
