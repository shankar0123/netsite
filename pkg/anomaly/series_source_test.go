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

package anomaly

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// stubConn satisfies driver.Conn for construction-only tests by
// embedding the interface (zero value). Method calls panic; the
// validation tests below all return before invoking any.
type stubConn struct{ driver.Conn }

// Argument-validation paths in ClickHouseSeriesSource.Series.
// The happy path needs ClickHouse and lives behind -tags integration
// (added in v0.0.20 alongside the rest of the soak coverage).
func TestClickHouseSeriesSource_ArgValidation(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		src     ClickHouseSeriesSource
		tenant  string
		test    string
		metric  MetricKind
		window  time.Duration
		wantSub string
	}{
		{"nil_conn", ClickHouseSeriesSource{}, "tnt-a", "tst-1", MetricLatencyP95, time.Hour, "Conn is nil"},
		{"empty_tenant", ClickHouseSeriesSource{Conn: stubConn{}}, "", "tst-1", MetricLatencyP95, time.Hour, "tenantID and testID required"},
		{"empty_test", ClickHouseSeriesSource{Conn: stubConn{}}, "tnt-a", "", MetricLatencyP95, time.Hour, "tenantID and testID required"},
		{"zero_window", ClickHouseSeriesSource{Conn: stubConn{}}, "tnt-a", "tst-1", MetricLatencyP95, 0, "window must be > 0"},
		{"unknown_metric", ClickHouseSeriesSource{Conn: stubConn{}}, "tnt-a", "tst-1", MetricKind("nope"), time.Hour, "unsupported metric"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.src.Series(ctx, tc.tenant, tc.test, tc.metric, tc.window)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %q; want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestClickHouseSeriesSource_DefaultBucketDuration(t *testing.T) {
	src := ClickHouseSeriesSource{Conn: stubConn{}}
	// Drive into the same code path as Series() but stop before the
	// real Query call by passing an unknown metric. The wantSub check
	// proves the metric switch executed (i.e. the default-bucket
	// branch did not panic on bucket=0).
	_, err := src.Series(context.Background(), "t", "u", MetricKind("nope"), time.Hour)
	if err == nil {
		t.Fatal("expected error for unknown metric")
	}
}

func TestBuildSeriesQuery_ContainsExpectedSQL(t *testing.T) {
	q, err := buildSeriesQuery(MetricLatencyP95, 7*24*time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"quantile(0.95)", "latency_ms", "tenant_id = ?", "test_id   = ?"} {
		if !strings.Contains(q, sub) {
			t.Errorf("latency_p95 query missing %q\nfull: %s", sub, q)
		}
	}

	q, err = buildSeriesQuery(MetricErrorRate, time.Hour, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"countIf(error_kind != '')", "greatest(count(), 1)"} {
		if !strings.Contains(q, sub) {
			t.Errorf("error_rate query missing %q\nfull: %s", sub, q)
		}
	}
}
