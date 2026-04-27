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
//   - A periodic Evaluator goroutine (v0.0.19) that pulls a
//     ClickHouse-backed SeriesSource for every enabled test, runs
//     Detect, and persists the Verdict to a Postgres-backed Store.
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
