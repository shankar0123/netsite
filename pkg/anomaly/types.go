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

// Package anomaly is NetSite's seasonal-aware anomaly detection
// engine. It accepts a time-stamped numeric series, fits a forecast,
// computes residuals, and reports whether the most recent point is
// anomalous — with explainable output (which method, which residual,
// which threshold, whether a calendar window suppressed the alert).
//
// What:
//   - Holt-Winters triple-exponential smoothing for series with
//     stable level/trend/season components.
//   - Classical seasonal decomposition (a simplification of STL,
//     using moving averages instead of LOESS) for series where the
//     seasonal shape varies and we want trend/season/residual
//     separation rather than a one-step forecast.
//   - Calendar suppression to silence alerts during operator-marked
//     windows (weekends, scheduled maintenance, holidays).
//   - A Detector that picks the right method by data density and
//     seasonality presence, then runs the calendar filter.
//
// How: every detector method takes (Series, Config) and returns a
// Verdict explaining what it concluded and why. The Verdict carries
// enough provenance for an operator to understand the decision
// without re-running the math.
//
// Why this design and not "z-score over a window": z-score detection
// works for stationary series with no seasonality. Real NetSite
// series are seasonal — canary success rate at 03:00 looks different
// from 12:00, and Saturday looks different from Tuesday. A naive
// z-score either alerts every Saturday morning (false positives) or
// is blunted to the point where real outages slip through. Seasonal
// methods are the standard answer; the SRE community converged on
// Holt-Winters and STL years ago. We adopt them here.
//
// Why we ship a simplified STL (not full STL-LOESS) in v0.0.8: STL-
// LOESS is well-defined but a substantial implementation (~500 lines
// of careful numerics). Classical seasonal decomposition (moving
// average for trend, period averages for season, residuals from
// both) gets us 80 % of the operational value at 10 % of the
// implementation cost. We document this trade-off in
// docs/algorithms/anomaly-detection.md and revisit when calibration
// data from real deployments justifies the upgrade (Phase 1+).
package anomaly

import (
	"errors"
	"time"
)

// Method identifies which detector method produced a Verdict.
type Method string

// Canonical Method values.
const (
	MethodHoltWinters       Method = "holt_winters"
	MethodSeasonalDecompose Method = "seasonal_decompose"
	MethodInsufficientData  Method = "insufficient_data"
)

// Severity grades how far the latest point is from the model's
// expectation, in units of MAD (median absolute deviation) of the
// fit residuals. Operators tune the breakpoints if their data is
// noisier than typical.
type Severity string

// Canonical severity values.
const (
	SeverityNone     Severity = "none"     // residual within normal band
	SeverityWatch    Severity = "watch"    // 3..5 MAD
	SeverityAnomaly  Severity = "anomaly"  // 5..8 MAD
	SeverityCritical Severity = "critical" // > 8 MAD
)

// Point is one observation in the input series.
type Point struct {
	At    time.Time
	Value float64
}

// Series is the input the detector consumes. The slice must be
// sorted ascending by At; detectors do not re-sort to keep their
// own runtime predictable. Callers reading from ClickHouse can rely
// on `ORDER BY observed_at ASC` to satisfy this.
type Series []Point

// Config tunes the detector. Zero values use the documented defaults.
type Config struct {
	// Period is the seasonality period in samples (NOT seconds).
	// For 30-second canary data sampled hourly seasonality is 120;
	// for daily-seasonality hourly data it's 24. The Detector picks
	// a sensible default if Period is zero (24 — assumes daily
	// seasonality on hourly samples).
	Period int

	// Alpha (level), Beta (trend), Gamma (season) smoothing
	// parameters for Holt-Winters. Zero means "use the default 0.3,
	// 0.1, 0.1". Domain experts tune these per series.
	Alpha, Beta, Gamma float64

	// MADThresholdWatch / Anomaly / Critical are the residual
	// breakpoints in MAD units. Defaults: 3, 5, 8.
	MADThresholdWatch    float64
	MADThresholdAnomaly  float64
	MADThresholdCritical float64

	// MinSamples is the minimum series length below which the
	// detector returns MethodInsufficientData. Default = 3 * Period
	// so we have at least three full seasonal cycles to fit on.
	MinSamples int

	// Calendar lists operator-marked suppression windows. A point
	// inside any of these intervals is reported with Suppressed=true
	// and never crosses the SeverityAnomaly threshold even if the
	// math says it should. Optional.
	Calendar []SuppressionWindow
}

// Verdict is what the detector returns. Every field exists to make
// the decision auditable: when an operator asks "why did this fire",
// the Verdict says which method ran, what the residual was, which
// threshold it crossed, and whether a calendar window applied.
type Verdict struct {
	Method      Method
	Severity    Severity
	Suppressed  bool      // true when a Calendar window covers the latest point
	LatestPoint Point     // mirrors the last Series entry
	Forecast    float64   // model's expectation for the latest point
	Residual    float64   // LatestPoint.Value - Forecast
	MAD         float64   // median absolute deviation of fit residuals
	MADUnits    float64   // |Residual| / MAD; 0 when MAD is 0
	Reason      string    // free-text explanation aimed at humans
	EvaluatedAt time.Time // server time at evaluation, UTC
}

// Sentinel errors. Detectors return these directly so callers can
// distinguish causes via errors.Is.
var (
	ErrEmptySeries      = errors.New("anomaly: empty series")
	ErrSeriesNotSorted  = errors.New("anomaly: series not sorted by At ascending")
	ErrInvalidPeriod    = errors.New("anomaly: period must be > 1")
	ErrInsufficientData = errors.New("anomaly: not enough samples for the chosen method")
)

// Defaults fills in the zero-value gaps of a user-supplied Config.
// Callers that want to see what the detector will actually run with
// (e.g., for echoing back in an API response) can call Defaults
// directly without invoking Detect.
func Defaults(cfg Config) Config {
	if cfg.Period <= 1 {
		cfg.Period = 24
	}
	if cfg.Alpha == 0 {
		cfg.Alpha = 0.3
	}
	if cfg.Beta == 0 {
		cfg.Beta = 0.1
	}
	if cfg.Gamma == 0 {
		cfg.Gamma = 0.1
	}
	if cfg.MADThresholdWatch == 0 {
		cfg.MADThresholdWatch = 3
	}
	if cfg.MADThresholdAnomaly == 0 {
		cfg.MADThresholdAnomaly = 5
	}
	if cfg.MADThresholdCritical == 0 {
		cfg.MADThresholdCritical = 8
	}
	if cfg.MinSamples == 0 {
		cfg.MinSamples = 3 * cfg.Period
	}
	return cfg
}
