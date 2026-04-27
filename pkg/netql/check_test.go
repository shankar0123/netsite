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
	"testing"
)

// mustParse is a test helper.
func mustParse(t *testing.T, src string) *Query {
	t.Helper()
	q, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return q
}

// TestCheck_NilArgs covers the defensive guards.
func TestCheck_NilArgs(t *testing.T) {
	if err := Check(nil, DefaultRegistry()); err == nil {
		t.Error("Check(nil, reg) returned nil")
	}
	q, _ := Parse("count")
	if err := Check(q, nil); err == nil {
		t.Error("Check(q, nil) returned nil")
	}
}

// TestCheck_UnknownMetric returns TypeError.
func TestCheck_UnknownMetric(t *testing.T) {
	q := mustParse(t, "banana")
	err := Check(q, DefaultRegistry())
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v; want *TypeError", err)
	}
}

// TestCheck_UngroupableColumn rejects grouping latency_p95 by
// error_kind (per the registry).
func TestCheck_UngroupableColumn(t *testing.T) {
	q := mustParse(t, "latency_p95 by error_kind")
	err := Check(q, DefaultRegistry())
	if err == nil {
		t.Fatal("expected TypeError on ungroupable column")
	}
}

// TestCheck_UnfilterableColumn rejects filtering by tenant_id
// (intentionally not in the registry).
func TestCheck_UnfilterableColumn(t *testing.T) {
	q := mustParse(t, "count where tenant_id = 'tnt-default'")
	err := Check(q, DefaultRegistry())
	if err == nil {
		t.Fatal("expected TypeError on unfilterable column")
	}
}

// TestCheck_WrongOpForFieldKind rejects `<` against a string column.
func TestCheck_WrongOpForFieldKind(t *testing.T) {
	q := mustParse(t, "count where pop < 'a'")
	err := Check(q, DefaultRegistry())
	if err == nil {
		t.Fatal("expected TypeError on string < operator")
	}
}

// TestCheck_BadOrderField rejects ordering by something that isn't
// in SELECT.
func TestCheck_BadOrderField(t *testing.T) {
	q := mustParse(t, "count by pop order by error_kind")
	err := Check(q, DefaultRegistry())
	if err == nil {
		t.Fatal("expected TypeError on bad order-by field")
	}
}

// TestCheck_NegativeLimit rejects limit ≤ 0.
func TestCheck_NegativeLimit(t *testing.T) {
	// Limit zero is impossible from the grammar (parseDurationToken
	// guards >0 only for durations; limit accepts any int). Force
	// it via a synthetic AST.
	zero := 0
	q := &Query{Metric: "count", Limit: &zero}
	err := Check(q, DefaultRegistry())
	if err == nil {
		t.Fatal("expected TypeError on limit <= 0")
	}
}

// TestCheck_HappyPath_AllRegistryShapes round-trips one query
// against each metric.
func TestCheck_HappyPath_AllRegistryShapes(t *testing.T) {
	cases := []string{
		"success_rate by pop where target = 'api.example.com' over 1h",
		"latency_p95 by pop, target where pop = 'pop-lhr-01' over 24h",
		"count by error_kind where kind = 'http' over 7d",
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
