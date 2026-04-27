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

package netql

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTranslateCH_Corpus walks every (.netql, .ch.sql) pair under
// testdata/ and asserts the translator output matches the snapshot.
//
// Workflow when a snapshot is intentionally updated:
//  1. change the translator;
//  2. run `go test -update` (or just edit the .ch.sql by hand);
//  3. eyeball the diff, commit both the code and the .ch.sql.
//
// Snapshot tests are the cheapest way to catch unintended SQL
// drift; the cost is one extra file per query, paid once per
// canonical example.
func TestTranslateCH_Corpus(t *testing.T) {
	matches, err := filepath.Glob("testdata/*.netql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no testdata/*.netql files found")
	}
	reg := DefaultRegistry()
	for _, srcPath := range matches {
		srcPath := srcPath
		name := strings.TrimSuffix(filepath.Base(srcPath), ".netql")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read source: %v", err)
			}
			snapPath := filepath.Join("testdata", name+".ch.sql")
			snap, err := os.ReadFile(snapPath)
			if err != nil {
				t.Fatalf("read snapshot %s: %v", snapPath, err)
			}
			q, err := Parse(strings.TrimSpace(string(src)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			out, err := TranslateClickHouse(q, reg, "tnt-test")
			if err != nil {
				t.Fatalf("TranslateClickHouse: %v", err)
			}
			gotSQL := strings.TrimRight(out.SQL, "\n")
			wantSQL := strings.TrimRight(string(snap), "\n")
			if gotSQL != wantSQL {
				t.Errorf("SQL mismatch for %s\n--- want\n%s\n--- got\n%s", name, wantSQL, gotSQL)
			}
		})
	}
}

// TestTranslateCH_TenantArgIsFirst asserts the tenantID always lands
// as $1 — security-critical invariant.
func TestTranslateCH_TenantArgIsFirst(t *testing.T) {
	q := mustParse(t, "count where pop = 'pop-lhr-01'")
	out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	if err != nil {
		t.Fatalf("TranslateClickHouse: %v", err)
	}
	if len(out.Args) == 0 || out.Args[0] != "tnt-X" {
		t.Errorf("Args[0] = %v; want tnt-X", out.Args)
	}
	if !strings.Contains(out.SQL, "tenant_id = $1") {
		t.Errorf("SQL missing tenant_id = $1: %s", out.SQL)
	}
}

// TestTranslateCH_DefaultOverHours asserts that omitting `over`
// yields the documented 1-hour default.
func TestTranslateCH_DefaultOverHours(t *testing.T) {
	q := mustParse(t, "count")
	out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	if err != nil {
		t.Fatalf("TranslateClickHouse: %v", err)
	}
	if !strings.Contains(out.SQL, "INTERVAL 1 HOUR") {
		t.Errorf("default over not applied: %s", out.SQL)
	}
}

// TestTranslateCH_DefaultLimit asserts the 10000 row cap.
func TestTranslateCH_DefaultLimit(t *testing.T) {
	q := mustParse(t, "count")
	out, _ := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	if !strings.Contains(out.SQL, "LIMIT 10000") {
		t.Errorf("default limit missing: %s", out.SQL)
	}
}

// TestTranslateCH_ContainsAndMatches covers the two regex/substring
// operators specifically.
func TestTranslateCH_ContainsAndMatches(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"count where target contains 'api'", "position(target, $2) > 0"},
		{"count where target matches '^api\\.'", "match(target, $2)"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			q := mustParse(t, tc.query)
			out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
			if err != nil {
				t.Fatalf("TranslateClickHouse: %v", err)
			}
			if !strings.Contains(out.SQL, tc.want) {
				t.Errorf("SQL %q missing %q", out.SQL, tc.want)
			}
		})
	}
}

// TestTranslateCH_CheckRunsFirst asserts type errors propagate.
func TestTranslateCH_CheckRunsFirst(t *testing.T) {
	q := mustParse(t, "banana")
	_, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TypeError", err)
	}
}

// TestTranslateCH_BackendMismatch asserts a Prometheus-backed metric
// (when added) cannot be translated to ClickHouse.
func TestTranslateCH_BackendMismatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MetricSpec{
		Name:    "fake_promql",
		Backend: BackendPrometheus,
		Source:  "n/a",
	})
	q := &Query{Metric: "fake_promql"}
	_, err := TranslateClickHouse(q, reg, "tnt-X")
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestRegistry_NamesAndGet covers the registry helpers.
func TestRegistry_NamesAndGet(t *testing.T) {
	reg := DefaultRegistry()
	names := reg.Names()
	if len(names) < 3 {
		t.Errorf("Names() returned %v; want ≥3 (success_rate, latency_p95, count)", names)
	}
	if _, ok := reg.Get("count"); !ok {
		t.Error("Get(count) = false; want true")
	}
	if _, ok := reg.Get("nonexistent"); ok {
		t.Error("Get(nonexistent) = true; want false")
	}
}
