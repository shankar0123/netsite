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
