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
	"log/slog"
	"time"
)

// What: the evaluator goroutine. On each tick (default 5 minutes,
// because real anomaly signal lives at minutes-to-hours granularity
// and ticking faster wastes ClickHouse cycles), for every enabled
// test:
//   1. Pull a Series via the SeriesSource (default = 7-day trailing
//      window, hourly buckets).
//   2. Call Detect(series, cfg) to produce a Verdict.
//   3. UpsertVerdict on the Postgres store.
//   4. Log a transition when severity changed since the last run.
//
// How: mirrors pkg/slo.Evaluator's tick model — one shared goroutine
// for the whole control plane, error-per-iteration is logged but
// doesn't stop the loop. The evaluator stays narrow (no notifier yet
// — that lands in v0.0.20 alongside the full anomaly_events history
// table).
//
// Why a separate evaluator goroutine, not a background per-test job:
// real-world test counts are dozens, not thousands; a single ticker
// is simpler than spawning a goroutine per test (and avoids the
// "what if a test is added at runtime" question — the next tick
// just picks it up because ListEnabledTests is re-queried every
// tick). Phase 1 revisits if scale ever demands sharding.

// Reader is the read-side dependency the evaluator needs. Defined
// here (not in store.go) so unit tests can pass an in-memory fake
// without dragging in pgxpool.
type Reader interface {
	ListEnabledTests(ctx context.Context) ([]TestRef, error)
	GetVerdict(ctx context.Context, tenantID, testID, metric string) (VerdictRow, error)
}

// Writer is the write-side dependency. Same rationale.
type Writer interface {
	UpsertVerdict(ctx context.Context, row VerdictRow) error
}

// EvaluatorConfig tunes the evaluator without forcing every caller
// to construct full Config blocks for HW/STL/calendar. Zero-value
// fields fall back to documented defaults.
type EvaluatorConfig struct {
	// Interval is how often the evaluator wakes up. Default 5m. We
	// deliberately do not run faster: anomaly evidence (a ~10 min
	// outage worth flagging) shows up clearly at hourly buckets, and
	// ticking every 30s would query ClickHouse 600 times to surface
	// a signal that one query already revealed.
	Interval time.Duration

	// Window is the trailing window the SeriesSource fetches.
	// Default 7 days = 168 hourly samples = 7 daily-seasonality
	// cycles, well above the detector's MinSamples=72 floor.
	Window time.Duration

	// Bucket is the GROUP BY granularity for the series. Default 1h.
	// Smaller buckets mean more samples (better statistics) at the
	// cost of more ClickHouse work and noisier residuals.
	Bucket time.Duration

	// Metrics are the per-test MetricKinds to evaluate. Default
	// {MetricLatencyP95}. Operators add MetricErrorRate when their
	// canaries are noisy enough that latency alone misses real
	// failures.
	Metrics []MetricKind

	// DetectorConfig is forwarded to Detect() per call. Zero values
	// pick up Defaults() in the detector. Period defaults to 24
	// (assumes daily seasonality on hourly samples) which is exactly
	// what our default Window/Bucket combination produces.
	DetectorConfig Config
}

// Defaults fills in zero-value gaps without mutating the caller's
// EvaluatorConfig. Returns the populated copy.
func (c EvaluatorConfig) Defaults() EvaluatorConfig {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.Window <= 0 {
		c.Window = 7 * 24 * time.Hour
	}
	if c.Bucket <= 0 {
		c.Bucket = time.Hour
	}
	if len(c.Metrics) == 0 {
		c.Metrics = []MetricKind{MetricLatencyP95}
	}
	return c
}

// Evaluator periodically re-scores every enabled test.
type Evaluator struct {
	Logger *slog.Logger
	Reader Reader
	Writer Writer
	Source SeriesSource
	Cfg    EvaluatorConfig

	// now is injected for tests; production uses time.Now.
	now func() time.Time
}

// NewEvaluator wires defaults and validates required dependencies.
func NewEvaluator(logger *slog.Logger, r Reader, w Writer, src SeriesSource, cfg EvaluatorConfig) (*Evaluator, error) {
	if logger == nil {
		return nil, errors.New("anomaly: nil logger")
	}
	if r == nil {
		return nil, errors.New("anomaly: nil reader")
	}
	if w == nil {
		return nil, errors.New("anomaly: nil writer")
	}
	if src == nil {
		return nil, errors.New("anomaly: nil source")
	}
	return &Evaluator{
		Logger: logger,
		Reader: r,
		Writer: w,
		Source: src,
		Cfg:    cfg.Defaults(),
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

// Run blocks until ctx is cancelled, ticking at e.Cfg.Interval.
// Mirrors pkg/slo.Evaluator.Run — fires once immediately so a fresh
// boot has anomaly state visible without waiting an Interval.
func (e *Evaluator) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.Cfg.Interval)
	defer ticker.Stop()
	e.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

// tick evaluates every (enabled test × configured metric) once.
// One bad test does not stop the rest; per-test errors are logged
// at Warn so an operator sees them without grep -i error.
func (e *Evaluator) tick(ctx context.Context) {
	tests, err := e.Reader.ListEnabledTests(ctx)
	if err != nil {
		e.Logger.Warn("anomaly: list enabled tests failed", slog.Any("err", err))
		return
	}
	for _, t := range tests {
		for _, m := range e.Cfg.Metrics {
			if err := e.EvaluateOne(ctx, t, m); err != nil {
				e.Logger.Warn("anomaly: evaluate one",
					slog.String("tenant_id", t.TenantID),
					slog.String("test_id", t.TestID),
					slog.String("metric", string(m)),
					slog.Any("err", err))
			}
		}
	}
}

// EvaluateOne is the single-(test, metric) entry point exposed for
// tests. Pulls a series, runs Detect, persists the verdict, logs a
// transition when severity changed.
func (e *Evaluator) EvaluateOne(ctx context.Context, t TestRef, metric MetricKind) error {
	series, err := e.Source.Series(ctx, t.TenantID, t.TestID, metric, e.Cfg.Window)
	if err != nil {
		return err
	}

	// Empty series: nothing to detect against. We still write a
	// verdict so the API surface can show "no data" explicitly
	// rather than 404.
	if len(series) == 0 {
		now := e.now()
		row := VerdictRow{
			TenantID:    t.TenantID,
			TestID:      t.TestID,
			Metric:      string(metric),
			Method:      MethodInsufficientData,
			Severity:    SeverityNone,
			LastPointAt: now,
			EvaluatedAt: now,
			Reason:      "no samples in window",
		}
		return e.recordTransition(ctx, row)
	}

	v, err := Detect(series, e.Cfg.DetectorConfig)
	if err != nil {
		return err
	}

	row := VerdictRow{
		TenantID:    t.TenantID,
		TestID:      t.TestID,
		Metric:      string(metric),
		Method:      v.Method,
		Severity:    v.Severity,
		Suppressed:  v.Suppressed,
		LastValue:   v.LatestPoint.Value,
		Forecast:    v.Forecast,
		Residual:    v.Residual,
		MAD:         v.MAD,
		MADUnits:    v.MADUnits,
		Reason:      v.Reason,
		LastPointAt: v.LatestPoint.At.UTC(),
		EvaluatedAt: e.now(),
	}
	return e.recordTransition(ctx, row)
}

// recordTransition upserts the verdict and, when the severity
// transitioned to a non-none level, logs at Info so an operator can
// grep the controlplane log for "anomaly transition" and find every
// detection. Phase 0.20 wires this to a Notifier; for now log-only.
func (e *Evaluator) recordTransition(ctx context.Context, row VerdictRow) error {
	prev, err := e.Reader.GetVerdict(ctx, row.TenantID, row.TestID, row.Metric)
	prevSeverity := SeverityNone
	if err == nil {
		prevSeverity = prev.Severity
	} else if !errors.Is(err, ErrVerdictNotFound) {
		// Read failure on the previous row is non-fatal — we still
		// want to upsert the new verdict. Log and continue.
		e.Logger.Warn("anomaly: GetVerdict failed (continuing with upsert)",
			slog.String("tenant_id", row.TenantID),
			slog.String("test_id", row.TestID),
			slog.String("metric", row.Metric),
			slog.Any("err", err))
	}

	if err := e.Writer.UpsertVerdict(ctx, row); err != nil {
		return err
	}

	if row.Severity != prevSeverity && row.Severity != SeverityNone {
		e.Logger.Info("anomaly: transition",
			slog.String("tenant_id", row.TenantID),
			slog.String("test_id", row.TestID),
			slog.String("metric", row.Metric),
			slog.String("from", string(prevSeverity)),
			slog.String("to", string(row.Severity)),
			slog.Bool("suppressed", row.Suppressed),
			slog.Float64("residual", row.Residual),
			slog.Float64("mad_units", row.MADUnits),
			slog.String("reason", row.Reason))
	}
	return nil
}
