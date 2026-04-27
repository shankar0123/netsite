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

// TestParse_MetricOnly covers the minimum-viable query.
func TestParse_MetricOnly(t *testing.T) {
	q, err := Parse("count")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Metric != "count" {
		t.Errorf("Metric = %q; want count", q.Metric)
	}
	if len(q.GroupBy) != 0 || q.Filter != nil || q.Over != nil ||
		q.Step != nil || q.OrderBy != nil || q.Limit != nil {
		t.Errorf("optional clauses leaked into AST: %+v", q)
	}
}

// TestParse_AllClauses exercises every grammar branch in one query.
func TestParse_AllClauses(t *testing.T) {
	src := "latency_p95 by pop, target where target = 'api.example.com' and not kind = 'dns' over 24h step 5m order by latency_p95 desc limit 100"
	q, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Metric != "latency_p95" {
		t.Errorf("Metric = %q", q.Metric)
	}
	if got, want := len(q.GroupBy), 2; got != want {
		t.Errorf("len(GroupBy) = %d; want %d", got, want)
	}
	if q.GroupBy[0] != "pop" || q.GroupBy[1] != "target" {
		t.Errorf("GroupBy = %v", q.GroupBy)
	}
	if q.Filter == nil {
		t.Fatal("Filter is nil")
	}
	if len(q.Filter.And) != 2 {
		t.Errorf("expected 2-term AND; got %+v", q.Filter)
	}
	if q.Over == nil || q.Over.Count != 24 || q.Over.Unit != "h" {
		t.Errorf("Over = %+v", q.Over)
	}
	if q.Step == nil || q.Step.Count != 5 || q.Step.Unit != "m" {
		t.Errorf("Step = %+v", q.Step)
	}
	if q.OrderBy == nil || q.OrderBy.Field != "latency_p95" || q.OrderBy.Direction != DirDesc {
		t.Errorf("OrderBy = %+v", q.OrderBy)
	}
	if q.Limit == nil || *q.Limit != 100 {
		t.Errorf("Limit = %v", q.Limit)
	}
}

// TestParse_InListString covers the `in (...)` value form.
func TestParse_InListString(t *testing.T) {
	q, err := Parse("count where pop in ('pop-lhr-01', 'pop-fra-01')")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pred := q.Filter.Predicate
	if pred == nil {
		t.Fatalf("expected predicate; got %+v", q.Filter)
	}
	if pred.Op != OpIn {
		t.Errorf("Op = %q; want in", pred.Op)
	}
	if pred.Value.Kind != ValStringList || len(pred.Value.Strings) != 2 {
		t.Errorf("Value = %+v", pred.Value)
	}
}

// TestParse_OrPrecedence asserts `a = 1 or b = 2 and c = 3` parses
// as `a or (b and c)` per the grammar (or-low, and-high).
func TestParse_OrPrecedence(t *testing.T) {
	q, err := Parse("count where pop = 'a' or pop = 'b' and target = 'c'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Filter.Or == nil || len(q.Filter.Or) != 2 {
		t.Fatalf("expected top-level Or with 2 terms; got %+v", q.Filter)
	}
	right := q.Filter.Or[1]
	if right.And == nil || len(right.And) != 2 {
		t.Errorf("right side should be 2-term AND; got %+v", right)
	}
}

// TestParse_NotApplies covers the `not <term>` shape.
func TestParse_NotApplies(t *testing.T) {
	q, err := Parse("count where not pop = 'pop-lhr-01'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Filter.Not == nil {
		t.Fatalf("expected Not; got %+v", q.Filter)
	}
	if q.Filter.Not.Predicate == nil {
		t.Errorf("Not.Predicate missing: %+v", q.Filter.Not)
	}
}

// TestParse_ParensOverridePrecedence asserts grouping flips
// or-low/and-high precedence.
func TestParse_ParensOverridePrecedence(t *testing.T) {
	q, err := Parse("count where (pop = 'a' or pop = 'b') and target = 'c'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Filter.And == nil || len(q.Filter.And) != 2 {
		t.Fatalf("expected top-level And with 2 terms; got %+v", q.Filter)
	}
	left := q.Filter.And[0]
	if left.Or == nil || len(left.Or) != 2 {
		t.Errorf("left should be Or; got %+v", left)
	}
}

// TestParse_OrderByDefaultAsc asserts the default direction is asc.
func TestParse_OrderByDefaultAsc(t *testing.T) {
	q, err := Parse("count by pop order by pop")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.OrderBy.Direction != DirAsc {
		t.Errorf("Direction = %q; want asc (default)", q.OrderBy.Direction)
	}
}

// TestParse_BadInput_MissingMetric rejects an empty or non-ident
// start.
func TestParse_BadInput_MissingMetric(t *testing.T) {
	cases := []string{
		"",
		"by pop",
		"24h",
		"'string'",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			_, err := Parse(src)
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Errorf("Parse(%q) err = %v; want *ParseError", src, err)
			}
		})
	}
}

// TestParse_BadInput_TrailingTokens rejects extra junk after a
// well-formed query.
func TestParse_BadInput_TrailingTokens(t *testing.T) {
	_, err := Parse("count over 1h limit 5 garbage")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v; want *ParseError", err)
	}
}

// TestParse_BadInput_LimitNotInt rejects `limit 'abc'`.
func TestParse_BadInput_LimitNotInt(t *testing.T) {
	_, err := Parse("count limit 'abc'")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v; want *ParseError", err)
	}
}

// TestParse_BadInput_EmptyGroupBy rejects `by` with no identifier.
func TestParse_BadInput_EmptyGroupBy(t *testing.T) {
	_, err := Parse("count by")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestParse_BadInput_BadOpKeyword rejects an unknown predicate
// operator keyword (random ident in op slot).
func TestParse_BadInput_BadOpKeyword(t *testing.T) {
	_, err := Parse("count where pop banana 'a'")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestParse_BadInput_DurationCountZero rejects `over 0h`.
func TestParse_BadInput_DurationCountZero(t *testing.T) {
	_, err := Parse("count over 0h")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v; want *ParseError", err)
	}
}

// TestParse_BadInput_StringMixedNumbers rejects mixed list types.
func TestParse_BadInput_StringMixedNumbers(t *testing.T) {
	_, err := Parse("count where pop in ('a', 1)")
	if err == nil {
		t.Fatal("expected error on mixed-type list")
	}
}
