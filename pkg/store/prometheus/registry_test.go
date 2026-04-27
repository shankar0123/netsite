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

package prometheus

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/prometheus/client_golang/prometheus"
)

// TestNewRegistry_HasGoAndProcessCollectors asserts the registry comes
// pre-populated with Go runtime metrics and process metrics. Without
// these, every NetSite binary would have a flat, useless dashboard
// for the first hour after launch.
func TestNewRegistry_HasGoAndProcessCollectors(t *testing.T) {
	reg := NewRegistry()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	cases := []struct {
		name string
		want string
	}{
		{"go runtime", "go_goroutines"},
		// process collector is best-effort on non-Linux; on the
		// platforms NetSite supports for production (Linux POPs and
		// the Linux control-plane), the FDs metric is always present.
		{"process FDs", "process_open_fds"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !mfsContains(mfs, tc.want) {
				t.Errorf("Gather did not return %q", tc.want)
			}
		})
	}
}

// TestNewRegistry_FreshPerCall asserts that two calls to NewRegistry
// return independent registries — registering a metric on one does
// not affect the other. This is the property that makes the
// per-process pattern test-safe.
func TestNewRegistry_FreshPerCall(t *testing.T) {
	a := NewRegistry()
	b := NewRegistry()

	cnt := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netsite_test_isolation_total",
	})
	a.MustRegister(cnt)

	// Registering the same Desc on b must not error: b has never seen
	// this metric. (If a and b shared global state, this would
	// duplicate-register.)
	b.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netsite_test_isolation_total",
	}))
}

// TestExposeOn_ErrorPaths asserts the validation guards return the
// expected sentinel errors instead of panicking.
func TestExposeOn_ErrorPaths(t *testing.T) {
	cases := []struct {
		name string
		mux  *http.ServeMux
		reg  *prometheus.Registry
		want error
	}{
		{"nil registry", http.NewServeMux(), nil, ErrNilRegistry},
		{"nil mux", nil, prometheus.NewRegistry(), ErrNilMux},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ExposeOn(tc.mux, tc.reg)
			if !errors.Is(err, tc.want) {
				t.Errorf("ExposeOn err = %v; want %v", err, tc.want)
			}
		})
	}
}

// TestExposeOn_ServesMetrics asserts the /metrics endpoint produces
// scrape-format output containing a known counter and the standard
// Go metric. End-to-end: register, expose, scrape, parse.
func TestExposeOn_ServesMetrics(t *testing.T) {
	reg := NewRegistry()
	cnt := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "netsite_test_e2e_total",
		Help: "End-to-end smoke counter for the prometheus exposer test.",
	})
	reg.MustRegister(cnt)
	cnt.Inc()
	cnt.Inc()
	cnt.Inc()

	mux := http.NewServeMux()
	if err := ExposeOn(mux, reg); err != nil {
		t.Fatalf("ExposeOn: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	got := string(body)
	cases := []struct {
		name string
		want string
	}{
		{"custom counter exposed", "netsite_test_e2e_total 3"},
		{"go runtime metric exposed", "go_goroutines"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("body missing %q. body[:200]=%q", tc.want, truncate(got, 200))
			}
		})
	}
}

// truncate returns the first n bytes of s for compact failure logs.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mfsContains reports whether the gathered metric families include
// one with the given fully-qualified metric name. Used to assert
// runtime/process collectors registered.
func mfsContains(mfs []*dto.MetricFamily, name string) bool {
	for _, mf := range mfs {
		if mf.GetName() == name {
			return true
		}
	}
	return false
}
