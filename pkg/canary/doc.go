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

// Package canary defines NetSite's synthetic-monitoring data model and
// the per-protocol Runner interfaces that POPs implement.
//
// What: a Test is a "what to check" record (target, protocol, schedule).
// A Result is a single execution outcome (timings, status, optional
// fingerprints). A Runner takes a Test and produces a Result.
//
// How: each protocol lives in its own subpackage (`dns`, `http`, `tls`)
// and exports a Runner that satisfies the interface here. The POP
// agent (pkg/popagent) maps Kind to the right Runner at execution
// time. Result fields cover the union of what every protocol can
// produce; protocols leave fields they cannot fill at the zero value
// — the ClickHouse `canary_results` schema (already in place via
// `0002_canary_results.sql`) treats those zeros as "not measured."
//
// Why a flat union-of-fields Result rather than per-protocol Result
// types: the storage destination is one ClickHouse table with one
// column-set. Threading per-protocol types through the publisher and
// ingestion pipeline would cost a per-protocol marshaler and a
// per-protocol Insert path for no operator-visible benefit. Union of
// fields keeps the wire format and the query layer trivially uniform.
//
// Why this package owns only the contracts: `pkg/canary/...` is
// imported by every POP protocol implementation and by the control-
// plane consumer that ingests Results into ClickHouse. Keeping it
// import-light (no protocol implementations live here) avoids cyclic
// imports as protocols add their own dependencies.
package canary
