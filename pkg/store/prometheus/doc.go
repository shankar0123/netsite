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

// Package prometheus is the in-process Prometheus registry and HTTP
// exposition surface for NetSite binaries.
//
// What: a per-process *prometheus.Registry with the standard Go
// runtime and process collectors registered, plus an HTTP handler to
// expose it on /metrics.
//
// How: NewRegistry() returns a fresh registry with collectors already
// installed. ExposeOn() mounts the /metrics handler on a caller-
// supplied http.ServeMux. Counters/gauges/histograms specific to a
// service are registered on the returned registry by service-level
// code.
//
// Why a per-process registry rather than the prometheus default
// global: tests need to construct fresh registries to avoid global
// state leaking between cases. The global registry is a hidden global
// variable and one of the most common sources of "test runs in
// isolation but fails in suite" bugs in Go.
//
// Why expose on a caller-supplied mux rather than starting a server
// here: NetSite binaries already run an HTTP server (control plane
// API on :8080, POP debug surface on :9100). Adding /metrics to the
// existing mux is one less listener to manage and matches what most
// production deployments configure for prom-scrape.
package prometheus
