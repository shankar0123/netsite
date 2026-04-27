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

package api

import (
	"strings"
	"testing"

	"github.com/shankar0123/netsite/pkg/netql"
)

// TestCompileNetQL_ClickHouse asserts the shared compile path
// returns SQL + args + the canonical metric metadata for a
// ClickHouse-backed query, with tenant scoping injected as $1.
func TestCompileNetQL_ClickHouse(t *testing.T) {
	out, err := compileNetQL(
		"latency_p95 by pop where target = 'api.example.com' over 24h",
		netql.DefaultRegistry(),
		"tnt-X",
	)
	if err != nil {
		t.Fatalf("compileNetQL: %v", err)
	}
	if out.Backend != string(netql.BackendClickHouse) {
		t.Errorf("Backend = %q; want clickhouse", out.Backend)
	}
	if out.Metric != "latency_p95" {
		t.Errorf("Metric = %q", out.Metric)
	}
	if !strings.Contains(out.SQL, "tenant_id = $1") {
		t.Errorf("SQL missing tenant_id = $1: %s", out.SQL)
	}
	if len(out.Args) == 0 || out.Args[0] != "tnt-X" {
		t.Errorf("Args[0] = %v; want tnt-X", out.Args)
	}
}

// TestCompileNetQL_PromQL asserts the Prometheus-backed branch
// returns a PromQL string and no SQL/Args.
func TestCompileNetQL_PromQL(t *testing.T) {
	out, err := compileNetQL("request_rate over 5m", netql.DefaultRegistry(), "tnt-X")
	if err != nil {
		t.Fatalf("compileNetQL: %v", err)
	}
	if out.Backend != string(netql.BackendPrometheus) {
		t.Errorf("Backend = %q; want prometheus", out.Backend)
	}
	if !strings.Contains(out.PromQL, "rate(netsite_http_requests_total") {
		t.Errorf("PromQL = %q; expected rate(...)", out.PromQL)
	}
	if out.SQL != "" || len(out.Args) != 0 {
		t.Errorf("PromQL response leaked SQL/Args: %+v", out)
	}
}

// TestCompileNetQL_Errors covers the three error classes — lex,
// parse, type.
func TestCompileNetQL_Errors(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"lex error (unterminated string)", "count where pop = 'oops"},
		{"parse error (missing operator)", "count where pop"},
		{"type error (unknown metric)", "banana over 1h"},
		{"type error (ungroupable column)", "latency_p95 by error_kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileNetQL(tc.query, netql.DefaultRegistry(), "tnt-X")
			if err == nil {
				t.Errorf("expected error for %q", tc.query)
			}
		})
	}
}
