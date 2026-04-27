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

// Package otel is NetSite's OpenTelemetry foundation. Every binary boots
// it; every later service emits spans, metrics, and propagates context
// through it.
//
// What: configures global tracer + meter providers backed by an OTLP
// gRPC exporter, plus context propagation via W3C Trace Context and
// Baggage. Returns a shutdown function the caller defers at process
// exit so in-flight spans flush before SIGTERM unblocks.
//
// How: Setup() reads a Config (typically from ConfigFromEnv), builds an
// otelsdk resource describing the process, wires up trace and metric
// pipelines, and installs them on the package-global otel handles. A
// disabled config returns no-op providers and a no-op shutdown — all
// instrumented call sites run unchanged when telemetry is off.
//
// Why a single Setup function rather than a struct that callers thread
// through: OpenTelemetry already treats the providers as process-global.
// Threading a NetSite-owned wrapper would not improve testability and
// would add a layer of indirection every caller has to learn. Tests
// that need isolation use TestingProvider() (see setup_test.go) which
// installs a non-global provider on a derived context.
//
// Why OTLP gRPC (not HTTP, not Jaeger, not Zipkin): OTLP is the
// vendor-neutral industry default; gRPC is faster than HTTP for the
// volumes NetSite produces (every canary, every BGP UPDATE, every flow
// record gets at least one span); switching exporter format later is a
// config change, not a code change. Decision D18 in PRD §11.
//
// Why parent-based ratio sampling defaulting to 1% head: 100% sampling
// at NetSite's eventual scale (canaries every 30s × thousands of tests
// × dozens of POPs) would dominate exporter cost without changing the
// useful-detection rate. Parent-based ensures a sampled trace stays
// sampled across services — partial traces are useless. The ratio is a
// runtime knob via NETSITE_OTEL_SAMPLING_RATIO so operators can crank
// to 100% during incidents.
package otel
