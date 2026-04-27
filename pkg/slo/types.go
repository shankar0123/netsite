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

// Package slo implements the SLO + multi-window multi-burn-rate
// alerting engine described in the Google SRE Workbook ch. 5.
//
// What: SLO definitions live in Postgres (slos / slo_state); the
// evaluator runs as a goroutine in the controlplane, periodically
// reading each SLO, computing its current burn rate over four
// canonical windows (1h, 5m, 6h, 30m), and firing a notifier on
// transitions into a burning state.
//
// How: the SLI today is the canary success rate — fraction of
// canary_results rows whose error_kind is empty over the requested
// window, optionally filtered by test_id / pop_id. Burn rate equals
// (1 - SLI) / (1 - objective). Two thresholds drive two alert
// classes: fast (1h+5m, default 14.4×, ~2% budget burned/hour) and
// slow (6h+30m, default 6.0×, ~5% budget burned over 6h). Both the
// long and short windows must exceed the threshold for the alert
// class to fire — that's the "multi-window" half of the design and
// what kills false positives from one-off transient spikes.
//
// Why this and not "alert when SLI < target": SRE Workbook ch. 5
// makes the decisive case — single-threshold alerts are either too
// noisy (alert on any breach) or too slow (alert when target busted
// outright). Multi-window multi-burn-rate is the standard answer
// across Google, Cloudflare, GitHub, and the Prometheus community.
// We adopt it directly. Algorithm rationale and the threshold-table
// derivation lives in docs/algorithms/multi-window-burn-rate.md.
package slo

import (
	"errors"
	"time"
)

// SLIKind identifies which class of indicator an SLO measures. We
// ship "canary_success" in v0.0.7; SLI kinds for HTTP latency
// percentile, BGP availability, and PCAP coverage land alongside
// their parent features.
type SLIKind string

// Canonical SLI kind values. The Postgres CHECK constraint must
// stay in sync.
const (
	SLIKindCanarySuccess SLIKind = "canary_success"
)

// Status is the evaluator's observed condition of an SLO.
//
//	unknown    — never evaluated.
//	no_data    — evaluated; the underlying window held no rows.
//	ok         — burn rate below all thresholds.
//	slow_burn  — both 6h-long and 30m-short above slow threshold.
//	fast_burn  — both 1h-long and 5m-short above fast threshold.
//
// fast_burn dominates slow_burn when both fire; we report the worst.
type Status string

// Status enum values. Mirrors the Postgres CHECK constraint.
const (
	StatusUnknown  Status = "unknown"
	StatusNoData   Status = "no_data"
	StatusOK       Status = "ok"
	StatusSlowBurn Status = "slow_burn"
	StatusFastBurn Status = "fast_burn"
)

// SLO is the operator-declared row in the slos table. Field
// semantics mirror the column documentation in 0005_slo.sql.
type SLO struct {
	ID                string
	TenantID          string
	Name              string
	Description       string
	SLIKind           SLIKind
	SLIFilter         map[string]any
	ObjectivePct      float64 // 0 < x < 1
	WindowSeconds     int64
	FastBurnThreshold float64 // default 14.4
	SlowBurnThreshold float64 // default 6.0
	NotifierURL       string  // optional; empty = log-only
	Enabled           bool
	CreatedAt         time.Time
}

// State is the evaluator's per-SLO record. Stored in the slo_state
// table; written on every evaluator tick.
type State struct {
	SLOID           string
	LastEvaluatedAt time.Time
	LastStatus      Status
	LastBurnRate    float64
	LastAlertedAt   time.Time
}

// BurnEvent is what the Notifier receives when an SLO crosses into a
// burning state. Operators wire this into PagerDuty, Slack, or
// whatever incident-management surface they use.
type BurnEvent struct {
	SLOID      string
	SLOName    string
	TenantID   string
	Severity   Status // StatusFastBurn or StatusSlowBurn
	BurnRate   float64
	Threshold  float64
	SLIValue   float64       // 0..1 success ratio over LongWindow
	LongWindow time.Duration // 1h for fast, 6h for slow
	OccurredAt time.Time
}

// Sentinel errors. Callers that need to distinguish causes for HTTP
// status mapping or retry decisions can use errors.Is.
var (
	// ErrSLONotFound is returned by Store.GetSLO when no row matches
	// the (tenant_id, id) lookup. Handlers map this to HTTP 404.
	ErrSLONotFound = errors.New("slo: not found")

	// ErrInvalidSLO is returned by Store.CreateSLO and Validate when
	// the input fails one of the structural checks (objective out of
	// range, threshold non-positive, unknown SLI kind).
	ErrInvalidSLO = errors.New("slo: invalid SLO definition")

	// ErrUnsupportedSLI is returned by the evaluator when an SLO
	// references an SLIKind it does not yet know how to compute.
	ErrUnsupportedSLI = errors.New("slo: unsupported SLI kind")
)

// Window constants. The four canonical windows of the multi-window
// multi-burn-rate scheme. Documented in
// docs/algorithms/multi-window-burn-rate.md.
const (
	WindowFastLong  = 1 * time.Hour
	WindowFastShort = 5 * time.Minute
	WindowSlowLong  = 6 * time.Hour
	WindowSlowShort = 30 * time.Minute
)

// Validate runs the structural checks Store.CreateSLO applies. Kept
// public so handlers can return 400 with a clear error before any DB
// round-trip.
func Validate(s SLO) error {
	if s.Name == "" {
		return errors.New("slo: name is required")
	}
	if s.SLIKind != SLIKindCanarySuccess {
		return ErrUnsupportedSLI
	}
	if s.ObjectivePct <= 0 || s.ObjectivePct >= 1 {
		return errors.New("slo: objective_pct must be in (0, 1)")
	}
	if s.WindowSeconds <= 0 {
		return errors.New("slo: window_seconds must be > 0")
	}
	if s.FastBurnThreshold <= 0 || s.SlowBurnThreshold <= 0 {
		return errors.New("slo: burn thresholds must be > 0")
	}
	return nil
}
