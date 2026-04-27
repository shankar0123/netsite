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

import (
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a new prometheus.Registry pre-populated with the
// standard Go runtime collector and the process collector. Service-
// specific metrics should be added to the returned registry via
// MustRegister.
//
// The returned registry has none of the default Go global collectors;
// it is hermetic. Two registries built in the same process produce
// independent metric sets.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	// Go runtime metrics: gc, memstats, goroutines, scheduler latency,
	// CPU. These give operators a default observability floor for free.
	reg.MustRegister(collectors.NewGoCollector())
	// Process-level metrics: open FDs, max FDs, virt/resident bytes,
	// CPU seconds. Available on Linux/macOS; on other platforms the
	// collector silently emits an empty set.
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// ExposeOn mounts an HTTP handler at /metrics on the provided
// *http.ServeMux that serves the Prometheus text-format scrape
// endpoint backed by reg.
//
// The handler is installed via mux.Handle, which means callers can
// register middleware (auth, slog, OTel) by composing the mux at the
// outer layer rather than wrapping promhttp.Handler here.
//
// Errors:
//   - ErrNilRegistry if reg is nil.
//   - ErrNilMux      if mux is nil.
func ExposeOn(mux *http.ServeMux, reg *prometheus.Registry) error {
	if reg == nil {
		return ErrNilRegistry
	}
	if mux == nil {
		return ErrNilMux
	}
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		// Gzip the response when clients support it. Reduces CPU on
		// the scraper side at the cost of small CPU here. The
		// promhttp default is to negotiate from Accept-Encoding.
		EnableOpenMetrics: true,
	}))
	return nil
}

// ErrNilRegistry is returned by ExposeOn when reg is nil.
var ErrNilRegistry = errors.New("prometheus: nil registry")

// ErrNilMux is returned by ExposeOn when mux is nil.
var ErrNilMux = errors.New("prometheus: nil mux")
