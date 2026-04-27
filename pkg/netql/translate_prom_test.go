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

// TestTranslateProm_Corpus walks each .prom.txt snapshot and asserts
// the translator output matches.
func TestTranslateProm_Corpus(t *testing.T) {
	matches, err := filepath.Glob("testdata/*.prom.txt")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no testdata/*.prom.txt files found")
	}
	reg := DefaultRegistry()
	for _, snapPath := range matches {
		snapPath := snapPath
		base := strings.TrimSuffix(filepath.Base(snapPath), ".prom.txt")
		srcPath := filepath.Join("testdata", base+".netql")
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read source %s: %v", srcPath, err)
			}
			snap, err := os.ReadFile(snapPath)
			if err != nil {
				t.Fatalf("read snapshot: %v", err)
			}
			q, err := Parse(strings.TrimSpace(string(src)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, err := TranslatePrometheus(q, reg)
			if err != nil {
				t.Fatalf("TranslatePrometheus: %v", err)
			}
			want := strings.TrimRight(string(snap), "\n")
			if got != want {
				t.Errorf("PromQL mismatch for %s\n--- want\n%s\n--- got\n%s", base, want, got)
			}
		})
	}
}

// TestTranslateProm_BackendMismatch asserts a ClickHouse-only metric
// is rejected.
func TestTranslateProm_BackendMismatch(t *testing.T) {
	q := mustParse(t, "count")
	_, err := TranslatePrometheus(q, DefaultRegistry())
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestTranslateProm_OrInFilterRejected — PromQL label matchers are
// AND-only.
func TestTranslateProm_OrInFilterRejected(t *testing.T) {
	q := mustParse(t, "request_rate where route = '/v1/health' or route = '/v1/auth/login'")
	_, err := TranslatePrometheus(q, DefaultRegistry())
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestTranslateProm_NotInFilterRejected — see comment in
// translate_prom.go for the rationale.
func TestTranslateProm_NotInFilterRejected(t *testing.T) {
	q := mustParse(t, "request_rate where not route = '/v1/health'")
	_, err := TranslatePrometheus(q, DefaultRegistry())
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestTranslateProm_ContainsLowersToRegex asserts contains→=~ with
// substring escaped.
func TestTranslateProm_ContainsLowersToRegex(t *testing.T) {
	q := mustParse(t, "request_rate where route contains '/v1/auth.'")
	got, err := TranslatePrometheus(q, DefaultRegistry())
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	// PromQL label values are double-quoted strings with Go-style
	// backslash escapes: `\\.` in the source produces `\.` in the
	// regex, which matches a literal dot. We assert the source-
	// level form (two backslashes before the dot).
	if !strings.Contains(got, `route=~".*/v1/auth\\..*"`) {
		t.Errorf("output missing escaped contains regex: %s", got)
	}
}

// TestTranslateProm_DefaultOver5m asserts that omitting `over`
// yields PromQL's idiomatic 5m default.
func TestTranslateProm_DefaultOver5m(t *testing.T) {
	q := mustParse(t, "request_rate")
	got, err := TranslatePrometheus(q, DefaultRegistry())
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	if !strings.Contains(got, "[5m]") {
		t.Errorf("default range missing: %s", got)
	}
}

// TestTranslateProm_MatchesPassesThroughRegex asserts `matches` is
// not double-escaped.
func TestTranslateProm_MatchesPassesThroughRegex(t *testing.T) {
	q := mustParse(t, "request_rate where route matches '^/v1/.*'")
	got, err := TranslatePrometheus(q, DefaultRegistry())
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	if !strings.Contains(got, `route=~"^/v1/.*"`) {
		t.Errorf("matches output wrong: %s", got)
	}
}

// TestTranslateProm_NeOperatorEmits asserts != becomes the PromQL !=
// matcher.
func TestTranslateProm_NeOperatorEmits(t *testing.T) {
	q := mustParse(t, "request_rate where status != '200'")
	got, err := TranslatePrometheus(q, DefaultRegistry())
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	if !strings.Contains(got, `status!="200"`) {
		t.Errorf("output missing != matcher: %s", got)
	}
}

// TestTranslateProm_NumericComparisonRejected asserts that <, >,
// etc. are rejected against label-typed columns.
func TestTranslateProm_NumericComparisonRejected(t *testing.T) {
	// status is FieldString in the registry but PromQL would still
	// reject `<` on a label even if the type-checker allowed it.
	// Add a hand-crafted spec to exercise this path.
	reg := NewRegistry()
	reg.Register(&MetricSpec{
		Name:    "fake",
		Backend: BackendPrometheus,
		GroupBy: map[string]string{"x": "x"},
		Filter:  map[string]FieldSpec{"x": {Kind: FieldNumber, Column: "x"}},
		Prom:    &PromSpec{MetricName: "fake_total", Aggregator: "sum", Function: "rate"},
	})
	q, err := Parse("fake where x < 1")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = TranslatePrometheus(q, reg)
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestTranslateProm_MissingPromSpec asserts a registry entry with
// Backend=Prometheus but Prom=nil errors clearly.
func TestTranslateProm_MissingPromSpec(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MetricSpec{
		Name:    "fake",
		Backend: BackendPrometheus,
	})
	q := &Query{Metric: "fake"}
	_, err := TranslatePrometheus(q, reg)
	var te *TranslateError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TranslateError", err)
	}
}

// TestTranslateProm_AndedPredicates asserts that an AND-shaped
// filter projects to a comma-separated PromQL label-matcher list.
func TestTranslateProm_AndedPredicates(t *testing.T) {
	q := mustParse(t, "request_rate where status = '500' and method = 'GET'")
	got, err := TranslatePrometheus(q, DefaultRegistry())
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	// Sorted alphabetically inside the matcher set.
	if !strings.Contains(got, `method="GET",status="500"`) {
		t.Errorf("AND-shape output wrong: %s", got)
	}
}

// TestCheck_OrAndNotHappyPaths walks the type-checker's recursive
// branches that the snapshot driver doesn't reach.
func TestCheck_OrAndNotHappyPaths(t *testing.T) {
	cases := []string{
		"count where pop = 'a' or pop = 'b'",
		"count where not pop = 'a'",
		"count where (pop = 'a' or pop = 'b') and target = 'c'",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			q := mustParse(t, src)
			if err := Check(q, DefaultRegistry()); err != nil {
				t.Errorf("Check: %v", err)
			}
		})
	}
}

// TestTranslateProm_RawGaugeNoFunction asserts that a metric with
// Function == "" lowers to a bare aggregator over the raw selector
// (no rate/increase wrapper).
func TestTranslateProm_RawGaugeNoFunction(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&MetricSpec{
		Name:    "queue_depth",
		Backend: BackendPrometheus,
		GroupBy: map[string]string{"shard": "shard"},
		Filter:  map[string]FieldSpec{"shard": {Kind: FieldString, Column: "shard"}},
		Prom: &PromSpec{
			MetricName: "netsite_queue_depth",
			Aggregator: "sum",
			Function:   "", // raw gauge
		},
	})
	q, err := Parse("queue_depth by shard")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, err := TranslatePrometheus(q, reg)
	if err != nil {
		t.Fatalf("TranslatePrometheus: %v", err)
	}
	want := "sum by (shard) (netsite_queue_depth)"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}
