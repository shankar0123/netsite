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
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"
)

// fakeReaderWriter implements both Reader and Writer in memory so
// the evaluator can be exercised without Postgres. Keyed by
// (tenant, test, metric) so tests can assert the exact upserts.
type fakeReaderWriter struct {
	mu       sync.Mutex
	tests    []TestRef
	verdicts map[string]VerdictRow
	listErr  error
}

func newFakeRW(tests []TestRef) *fakeReaderWriter {
	return &fakeReaderWriter{
		tests:    tests,
		verdicts: make(map[string]VerdictRow),
	}
}

func (f *fakeReaderWriter) key(tenant, test, metric string) string {
	return tenant + "|" + test + "|" + metric
}

func (f *fakeReaderWriter) ListEnabledTests(_ context.Context) ([]TestRef, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]TestRef, len(f.tests))
	copy(out, f.tests)
	return out, nil
}

func (f *fakeReaderWriter) GetVerdict(_ context.Context, tenant, test, metric string) (VerdictRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.verdicts[f.key(tenant, test, metric)]
	if !ok {
		return VerdictRow{}, ErrVerdictNotFound
	}
	return v, nil
}

func (f *fakeReaderWriter) UpsertVerdict(_ context.Context, row VerdictRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verdicts[f.key(row.TenantID, row.TestID, row.Metric)] = row
	return nil
}

// fakeSource serves a canned Series per (tenant, test, metric).
// Missing key → empty series, mirroring the "no data in window" path.
type fakeSource struct {
	mu     sync.Mutex
	series map[string]Series
	calls  int
	err    error
}

func newFakeSource() *fakeSource {
	return &fakeSource{series: make(map[string]Series)}
}

func (f *fakeSource) put(tenant, test string, metric MetricKind, s Series) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.series[tenant+"|"+test+"|"+string(metric)] = s
}

func (f *fakeSource) Series(_ context.Context, tenant, test string, metric MetricKind, _ time.Duration) (Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.series[tenant+"|"+test+"|"+string(metric)]; ok {
		return s, nil
	}
	return nil, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// evalSeasonalSeries builds a sinusoidal hourly series of length n
// samples, with daily period (24h), amplitude amp, and a small
// deterministic noise floor (~1% of amp) so MAD is non-zero. Without
// noise the detector's residual / MAD ratio explodes — every
// micro-deviation classifies as an extreme anomaly. This shape
// matches what real canary timeseries look like.
//
// The last point is offset by spike (in raw value units) so a test
// can demand "this should classify as anomaly" by passing
// spike >> amp.
func evalSeasonalSeries(n int, base, amp, spike float64) Series {
	now := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	out := make(Series, n)
	noiseAmp := amp * 0.05
	if noiseAmp == 0 {
		noiseAmp = 0.01
	}
	for i := 0; i < n; i++ {
		// Linear-congruential PRNG so the test is deterministic
		// across architectures (math/rand seeded to a fixed value
		// would also work; using the index keeps it inline).
		seed := uint64(i)*2862933555777941757 + 3037000493
		noise := (float64(seed%2000)/1000.0 - 1.0) * noiseAmp
		v := base + amp*math.Sin(2*math.Pi*float64(i)/24.0) + noise
		out[i] = Point{
			At:    now.Add(time.Duration(i) * time.Hour),
			Value: v,
		}
	}
	out[n-1].Value += spike
	return out
}

func TestNewEvaluator_NilDeps(t *testing.T) {
	logger := discardLogger()
	rw := newFakeRW(nil)
	src := newFakeSource()

	tests := []struct {
		name string
		l    *slog.Logger
		r    Reader
		w    Writer
		s    SeriesSource
	}{
		{"nil_logger", nil, rw, rw, src},
		{"nil_reader", logger, nil, rw, src},
		{"nil_writer", logger, rw, nil, src},
		{"nil_source", logger, rw, rw, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewEvaluator(tc.l, tc.r, tc.w, tc.s, EvaluatorConfig{}); err == nil {
				t.Fatalf("expected error for %q", tc.name)
			}
		})
	}
}

func TestEvaluatorConfig_Defaults(t *testing.T) {
	cfg := EvaluatorConfig{}.Defaults()
	if cfg.Interval != 5*time.Minute {
		t.Errorf("interval default: got %v want 5m", cfg.Interval)
	}
	if cfg.Window != 7*24*time.Hour {
		t.Errorf("window default: got %v want 168h", cfg.Window)
	}
	if cfg.Bucket != time.Hour {
		t.Errorf("bucket default: got %v want 1h", cfg.Bucket)
	}
	if len(cfg.Metrics) != 1 || cfg.Metrics[0] != MetricLatencyP95 {
		t.Errorf("metrics default: got %v want [latency_p95]", cfg.Metrics)
	}

	// Explicit values must survive Defaults().
	custom := EvaluatorConfig{
		Interval: time.Minute,
		Window:   2 * time.Hour,
		Bucket:   time.Minute,
		Metrics:  []MetricKind{MetricErrorRate},
	}.Defaults()
	if custom.Interval != time.Minute || custom.Window != 2*time.Hour ||
		custom.Bucket != time.Minute || custom.Metrics[0] != MetricErrorRate {
		t.Errorf("custom values not preserved: %+v", custom)
	}
}

func TestEvaluator_EvaluateOne_HappyPath(t *testing.T) {
	rw := newFakeRW(nil)
	src := newFakeSource()
	// 168 hourly samples, daily seasonality, no spike → severity = none.
	src.put("tnt-a", "tst-1", MetricLatencyP95, evalSeasonalSeries(168, 100, 5, 0))

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.EvaluateOne(context.Background(), TestRef{TenantID: "tnt-a", TestID: "tst-1"}, MetricLatencyP95); err != nil {
		t.Fatal(err)
	}

	got, err := rw.GetVerdict(context.Background(), "tnt-a", "tst-1", string(MetricLatencyP95))
	if err != nil {
		t.Fatalf("verdict not persisted: %v", err)
	}
	if got.Severity != SeverityNone {
		t.Errorf("severity: got %q want none", got.Severity)
	}
	if got.Method == MethodInsufficientData {
		t.Errorf("method: got insufficient_data, want a real method")
	}
	if got.Method != MethodHoltWinters && got.Method != MethodSeasonalDecompose {
		t.Errorf("unexpected method %q", got.Method)
	}
}

func TestEvaluator_EvaluateOne_AnomalySpike_FiresTransition(t *testing.T) {
	rw := newFakeRW(nil)
	src := newFakeSource()
	// Big spike (>> amplitude) on the latest point → expect severity != none.
	src.put("tnt-a", "tst-1", MetricLatencyP95, evalSeasonalSeries(168, 100, 5, 200))

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.EvaluateOne(context.Background(), TestRef{TenantID: "tnt-a", TestID: "tst-1"}, MetricLatencyP95); err != nil {
		t.Fatal(err)
	}
	got, err := rw.GetVerdict(context.Background(), "tnt-a", "tst-1", string(MetricLatencyP95))
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity == SeverityNone {
		t.Errorf("expected non-none severity for huge spike, got %q (residual=%.2f mad_units=%.2f reason=%q)",
			got.Severity, got.Residual, got.MADUnits, got.Reason)
	}
}

func TestEvaluator_EvaluateOne_EmptySeries_WritesInsufficientData(t *testing.T) {
	rw := newFakeRW(nil)
	src := newFakeSource() // no series put → returns empty

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.EvaluateOne(context.Background(), TestRef{TenantID: "tnt-a", TestID: "tst-1"}, MetricLatencyP95); err != nil {
		t.Fatal(err)
	}
	got, err := rw.GetVerdict(context.Background(), "tnt-a", "tst-1", string(MetricLatencyP95))
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != MethodInsufficientData {
		t.Errorf("method: got %q want insufficient_data", got.Method)
	}
	if got.Severity != SeverityNone {
		t.Errorf("severity: got %q want none", got.Severity)
	}
	if got.Reason == "" {
		t.Errorf("expected non-empty reason for insufficient_data")
	}
}

func TestEvaluator_Tick_IteratesEveryTestAndMetric(t *testing.T) {
	rw := newFakeRW([]TestRef{
		{TenantID: "tnt-a", TestID: "tst-1"},
		{TenantID: "tnt-a", TestID: "tst-2"},
		{TenantID: "tnt-b", TestID: "tst-3"},
	})
	src := newFakeSource()
	for _, ref := range rw.tests {
		src.put(ref.TenantID, ref.TestID, MetricLatencyP95, evalSeasonalSeries(168, 100, 5, 0))
		src.put(ref.TenantID, ref.TestID, MetricErrorRate, evalSeasonalSeries(168, 0.01, 0.001, 0))
	}

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{
		Metrics: []MetricKind{MetricLatencyP95, MetricErrorRate},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev.tick(context.Background())

	if got, want := src.calls, 3*2; got != want {
		t.Errorf("series calls: got %d want %d", got, want)
	}
	if got, want := len(rw.verdicts), 3*2; got != want {
		t.Errorf("verdicts persisted: got %d want %d", got, want)
	}
}

func TestEvaluator_Tick_ListErrorIsLoggedNotFatal(t *testing.T) {
	rw := newFakeRW(nil)
	rw.listErr = errors.New("postgres exploded")
	src := newFakeSource()

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	// Must not panic; must not call source.
	ev.tick(context.Background())
	if src.calls != 0 {
		t.Errorf("source called %d times despite list error", src.calls)
	}
	if len(rw.verdicts) != 0 {
		t.Errorf("verdicts written %d times despite list error", len(rw.verdicts))
	}
}

func TestEvaluator_Tick_PerTestErrorDoesNotStopIteration(t *testing.T) {
	rw := newFakeRW([]TestRef{
		{TenantID: "tnt-a", TestID: "tst-bad"},
		{TenantID: "tnt-a", TestID: "tst-ok"},
	})
	src := newFakeSource()
	// tst-bad gets no series put + we override err for the first call... simpler:
	// put a good series for tst-ok, leave tst-bad unset → empty series,
	// which the evaluator handles successfully (insufficient_data row).
	// Use the err knob to force a hard failure on every call instead.
	src.err = errors.New("clickhouse exploded")

	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ev.tick(context.Background())

	// Both tests should have been attempted (so source.calls == 2).
	if src.calls != 2 {
		t.Errorf("source calls: got %d want 2 (errors must not stop iteration)", src.calls)
	}
	if len(rw.verdicts) != 0 {
		t.Errorf("verdicts written despite source error: %d", len(rw.verdicts))
	}
}

func TestEvaluator_Run_StopsOnContextCancel(t *testing.T) {
	rw := newFakeRW(nil)
	src := newFakeSource()
	ev, err := NewEvaluator(discardLogger(), rw, rw, src, EvaluatorConfig{
		Interval: time.Millisecond, // very fast tick to make the test snappy
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ev.Run(ctx) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned err: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestBuildSeriesQuery_AllMetrics(t *testing.T) {
	for _, m := range []MetricKind{MetricLatencyP95, MetricErrorRate} {
		q, err := buildSeriesQuery(m, 7*24*time.Hour, time.Hour)
		if err != nil {
			t.Fatalf("metric %q: unexpected err %v", m, err)
		}
		if q == "" {
			t.Fatalf("metric %q: empty query", m)
		}
	}
	if _, err := buildSeriesQuery(MetricKind("does_not_exist"), time.Hour, time.Minute); err == nil {
		t.Errorf("expected error for unknown metric")
	}
}
