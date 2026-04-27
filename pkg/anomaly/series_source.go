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
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// What: a SeriesSource produces a Series for one (tenant, test,
// metric) over a trailing window. The evaluator calls into it once
// per tick per test. The default implementation is backed by
// ClickHouse against the canary_results table.
//
// How: the ClickHouse implementation issues one query per call:
// `SELECT bucket, agg(metric) FROM canary_results GROUP BY bucket
// ORDER BY bucket`. The bucket size and aggregation depend on the
// metric kind — for latency_p95 we GROUP BY hour and use
// quantile(0.95). For success_rate we GROUP BY hour and compute
// countIf(error_kind='') / count(). Buckets with no data are
// omitted (the detector's MinSamples gate handles sparse series).
//
// Why ClickHouse-side aggregation rather than raw rows: a 7-day
// window across all POPs for a busy test is hundreds of thousands
// of rows; aggregating server-side reduces that to ~168 (one per
// hour for 7 days). ClickHouse is built for this exact pattern;
// pulling raw rows over the network just to take a quantile in Go
// would be a waste.
//
// Why two interfaces (SeriesSource + the concrete CH impl) rather
// than just the concrete: tests inject a fake without dragging in
// a testcontainer — same shape as pkg/slo.SLISource.

// MetricKind identifies which canary_results column / aggregation
// the source pulls. Stored as a string so it can travel through
// API responses for transparency.
type MetricKind string

// Canonical metric kinds. Add new ones here + a case in the
// ClickHouseSeriesSource builder.
const (
	// MetricLatencyP95 is the p95 of canary_results.latency_ms over
	// the bucket. The detector treats spikes (positive residuals) as
	// the meaningful signal — slowdowns are anomalies, speed-ups are
	// not. (We still surface negative residuals; the operator decides
	// what to do with them.)
	MetricLatencyP95 MetricKind = "latency_p95"

	// MetricErrorRate is countIf(error_kind != '') / count() over the
	// bucket. The detector treats any deviation as meaningful — both
	// "more failures than expected" and "fewer failures than
	// expected" can indicate a problem (e.g. a silent regression
	// where the canary stops reporting but doesn't error).
	MetricErrorRate MetricKind = "error_rate"
)

// SeriesSource pulls a Series for one (tenant, test, metric) tuple
// over a trailing window. Implementations must return the series
// sorted ascending by Point.At.
type SeriesSource interface {
	Series(ctx context.Context, tenantID, testID string, metric MetricKind, window time.Duration) (Series, error)
}

// ClickHouseSeriesSource implements SeriesSource against the
// canary_results table. BucketDuration controls the GROUP BY
// granularity; defaults to 1 hour when zero.
type ClickHouseSeriesSource struct {
	Conn           driver.Conn
	BucketDuration time.Duration
}

// Series implements SeriesSource. Validates the metric kind first
// so a typo at the call site fails fast rather than executing a
// half-built query.
func (c ClickHouseSeriesSource) Series(ctx context.Context, tenantID, testID string, metric MetricKind, window time.Duration) (Series, error) {
	if c.Conn == nil {
		return nil, errors.New("anomaly: ClickHouseSeriesSource.Conn is nil")
	}
	if tenantID == "" || testID == "" {
		return nil, errors.New("anomaly: tenantID and testID required")
	}
	if window <= 0 {
		return nil, errors.New("anomaly: window must be > 0")
	}
	bucket := c.BucketDuration
	if bucket <= 0 {
		bucket = time.Hour
	}

	q, err := buildSeriesQuery(metric, window, bucket)
	if err != nil {
		return nil, err
	}

	rows, err := c.Conn.Query(ctx, q, tenantID, testID)
	if err != nil {
		return nil, fmt.Errorf("anomaly: clickhouse query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(Series, 0, 168)
	for rows.Next() {
		var (
			at  time.Time
			val float64
		)
		if err := rows.Scan(&at, &val); err != nil {
			return nil, fmt.Errorf("anomaly: scan row: %w", err)
		}
		out = append(out, Point{At: at.UTC(), Value: val})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("anomaly: rows err: %w", err)
	}
	return out, nil
}

// buildSeriesQuery returns a parameterised ClickHouse query for the
// requested metric. Window and bucket are inlined as INTERVAL
// constants because ClickHouse parameter substitution does not
// accept INTERVAL values; both are computed by the evaluator from
// internal constants and never come from user input.
//
// Tenant + test_id always bind via $1 and $2 — those DO come from
// the catalog (and ultimately authentication context) and we want
// SQL injection defence layered on top of the catalog's own
// validation.
func buildSeriesQuery(metric MetricKind, window, bucket time.Duration) (string, error) {
	bucketSecs := int64(bucket.Seconds())
	windowSecs := int64(window.Seconds())
	switch metric {
	case MetricLatencyP95:
		return fmt.Sprintf(`
            SELECT toStartOfInterval(observed_at, INTERVAL %d SECOND) AS bucket,
                   quantile(0.95)(latency_ms) AS v
            FROM canary_results
            WHERE tenant_id = ?
              AND test_id   = ?
              AND observed_at >= now() - INTERVAL %d SECOND
            GROUP BY bucket
            ORDER BY bucket`, bucketSecs, windowSecs), nil
	case MetricErrorRate:
		return fmt.Sprintf(`
            SELECT toStartOfInterval(observed_at, INTERVAL %d SECOND) AS bucket,
                   countIf(error_kind != '') / greatest(count(), 1) AS v
            FROM canary_results
            WHERE tenant_id = ?
              AND test_id   = ?
              AND observed_at >= now() - INTERVAL %d SECOND
            GROUP BY bucket
            ORDER BY bucket`, bucketSecs, windowSecs), nil
	default:
		return "", fmt.Errorf("anomaly: unsupported metric %q", metric)
	}
}
