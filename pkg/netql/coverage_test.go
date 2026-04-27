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
	"strings"
	"testing"
)

// This file is the gap-filling companion to the per-feature *_test.go
// files above. The tests here aren't structured around grammar
// branches — they explicitly walk paths the canonical tests
// don't, so the package clears the algorithm-package coverage floor
// of ≥95% (CLAUDE.md §Tests).

// TestParse_NumberInList exercises the number-list branch of
// parseValueList + bindList for ValNumberList.
func TestParse_NumberInList(t *testing.T) {
	// observed_at is the only numeric/duration filter on
	// canary_results in the default registry — use it for the round-
	// trip through Check + Translate.
	q, err := Parse("count where observed_at in (1, 2, 3)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Filter.Predicate.Value.Kind != ValNumberList {
		t.Errorf("Value.Kind = %d; want ValNumberList", q.Filter.Predicate.Value.Kind)
	}
	if got := len(q.Filter.Predicate.Value.Numbers); got != 3 {
		t.Errorf("len(Numbers) = %d; want 3", got)
	}
	out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	if err != nil {
		t.Fatalf("TranslateClickHouse: %v", err)
	}
	if !strings.Contains(out.SQL, "observed_at IN (") {
		t.Errorf("SQL missing observed_at IN list: %s", out.SQL)
	}
}

// TestParse_BadInput_NumberListBadComma covers the invalid-tail path
// in parseNumberList.
func TestParse_BadInput_NumberListBadComma(t *testing.T) {
	_, err := Parse("count where observed_at in (1, 'oops')")
	if err == nil {
		t.Fatal("expected parse error on bad number list")
	}
}

// TestErrorTypes_StringerCoverage hits all four custom error-type
// Error() methods directly so they don't sit at 0% coverage.
func TestErrorTypes_StringerCoverage(t *testing.T) {
	cases := []error{
		&LexError{Pos: 1, Msg: "x"},
		&ParseError{Pos: 2, Want: "y", Got: "z"},
		&TypeError{Reason: "r"},
		&TranslateError{Reason: "r"},
	}
	for _, e := range cases {
		if e.Error() == "" {
			t.Errorf("%T.Error() = empty", e)
		}
	}
}

// TestUnitToCH_AllUnits walks every unit conversion branch.
func TestUnitToCH_AllUnits(t *testing.T) {
	cases := map[string]string{
		"s":         "SECOND",
		"m":         "MINUTE",
		"h":         "HOUR",
		"d":         "DAY",
		"w":         "WEEK",
		"x":         "X", // default branch falls through to upper-case
		"unknown_x": "UNKNOWN_X",
	}
	for in, want := range cases {
		if got := unitToCH(in); got != want {
			t.Errorf("unitToCH(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestTranslateCH_AllOverUnits forces every unit through the
// translator.
func TestTranslateCH_AllOverUnits(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"count over 30s", "INTERVAL 30 SECOND"},
		{"count over 5m", "INTERVAL 5 MINUTE"},
		{"count over 24h", "INTERVAL 24 HOUR"},
		{"count over 7d", "INTERVAL 7 DAY"},
		{"count over 2w", "INTERVAL 2 WEEK"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			q := mustParse(t, tc.in)
			out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
			if err != nil {
				t.Fatalf("TranslateClickHouse: %v", err)
			}
			if !strings.Contains(out.SQL, tc.want) {
				t.Errorf("SQL missing %q: %s", tc.want, out.SQL)
			}
		})
	}
}

// TestNames_DiagnosticHelpers walks the three diagnostic-only
// stringers used in error messages.
func TestNames_DiagnosticHelpers(t *testing.T) {
	// tokenKindName covers
	for k := TokenKind(-1); k <= TokKeyword+1; k++ {
		_ = tokenKindName(k)
	}
	// fieldKindName covers
	for k := FieldKind(-1); k <= FieldDuration+1; k++ {
		_ = fieldKindName(k)
	}
	// valueKindName covers
	for k := ValueKind(-1); k <= ValNumberList+1; k++ {
		_ = valueKindName(k)
	}
}

// TestParse_AllOpKeywords forces every TokKeyword op into parseOp.
func TestParse_AllOpKeywords(t *testing.T) {
	cases := []string{
		"count where pop in ('x')",
		"count where target contains 'api'",
		"count where target matches '^api'",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			if _, err := Parse(src); err != nil {
				t.Errorf("Parse(%q): %v", src, err)
			}
		})
	}
}

// TestParse_OrderByDescExplicit forces the asc/desc directional
// branch.
func TestParse_OrderByDescExplicit(t *testing.T) {
	q := mustParse(t, "count by pop order by pop desc")
	if q.OrderBy.Direction != DirDesc {
		t.Errorf("Direction = %q; want desc", q.OrderBy.Direction)
	}
}

// TestCheck_NumberPredicateOK round-trips the numeric-predicate
// branch through opCompatible / checkPredicate.
func TestCheck_NumberPredicateOK(t *testing.T) {
	q := mustParse(t, "count where observed_at >= 100")
	if err := Check(q, DefaultRegistry()); err != nil {
		t.Errorf("Check: %v", err)
	}
}

// TestParse_BadInput_GroupByMissingComma rejects `by pop target`
// (operators must use `,` between group-by columns).
func TestParse_BadInput_GroupByMissingComma(t *testing.T) {
	// The parser will succeed parsing `count by pop`, and then
	// `target` becomes a trailing token that fails the EOF check.
	_, err := Parse("count by pop target")
	if err == nil {
		t.Fatal("expected error on missing comma between group-by columns")
	}
}

// TestParse_ErrorPaths exercises every error-return inside the
// parser. The bodies of these branches are tiny — one line each —
// but they collectively make up the bulk of the uncovered lines
// because they propagate child-parse errors. Hitting them takes
// targeted bad inputs.
func TestParse_ErrorPaths(t *testing.T) {
	cases := []string{
		// Parse propagates Lex errors.
		"count where x = 'unterminated",
		// `over` not followed by a duration.
		"count over now",
		// `step` not followed by a duration.
		"count step soon",
		// `order` not followed by `by`.
		"count order pop",
		// `order by` not followed by an identifier.
		"count order by 'oops'",
		// `where` followed by something that can't start a term.
		"count where over 1h",
		// `(` without matching `)`.
		"count where (pop = 'a' over 1h",
		// `in` without `(`.
		"count where pop in 'a'",
		// trailing comma in group-by.
		"count by pop,",
		// trailing comma in IN list.
		"count where pop in ('a',)",
		// `not` without a term following.
		"count where not",
		// empty IN list — lparen then rparen.
		"count where pop in ()",
		// number list that mixes in a string at a non-leading slot.
		"count where observed_at in (1, 'a')",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			if _, err := Parse(src); err == nil {
				t.Errorf("Parse(%q) returned nil; want error", src)
			}
		})
	}
}

// TestParse_AllComparisonOps exercises each of the six comparison
// operator branches in parseOp. parseValue's number scalar fires on
// each; `observed_at` is the only numeric column in the registry, so
// every one of these is also a Check happy path.
func TestParse_AllComparisonOps(t *testing.T) {
	cases := []struct {
		query   string
		wantOp  Op
		wantSQL string
	}{
		{"count where observed_at = 100", OpEq, "observed_at = $2"},
		{"count where observed_at != 100", OpNe, "observed_at != $2"},
		{"count where observed_at < 100", OpLt, "observed_at < $2"},
		{"count where observed_at <= 100", OpLe, "observed_at <= $2"},
		{"count where observed_at > 100", OpGt, "observed_at > $2"},
		{"count where observed_at >= 100", OpGe, "observed_at >= $2"},
	}
	for _, tc := range cases {
		t.Run(string(tc.wantOp), func(t *testing.T) {
			q := mustParse(t, tc.query)
			if q.Filter.Predicate.Op != tc.wantOp {
				t.Errorf("Op = %q; want %q", q.Filter.Predicate.Op, tc.wantOp)
			}
			out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
			if err != nil {
				t.Fatalf("TranslateClickHouse: %v", err)
			}
			if !strings.Contains(out.SQL, tc.wantSQL) {
				t.Errorf("SQL missing %q: %s", tc.wantSQL, out.SQL)
			}
		})
	}
}

// TestParse_StepClauseHappyPath asserts `step` round-trips into the
// AST so the translator can use it later.
func TestParse_StepClauseHappyPath(t *testing.T) {
	q := mustParse(t, "count over 24h step 5m")
	if q.Step == nil || q.Step.Count != 5 || q.Step.Unit != "m" {
		t.Errorf("Step = %+v", q.Step)
	}
}

// TestTranslateCH_NotPrintsCorrectly asserts the explicit NOT branch
// renders.
func TestTranslateCH_NotPrintsCorrectly(t *testing.T) {
	q := mustParse(t, "count where not pop = 'pop-lhr-01'")
	out, err := TranslateClickHouse(q, DefaultRegistry(), "tnt-X")
	if err != nil {
		t.Fatalf("TranslateClickHouse: %v", err)
	}
	if !strings.Contains(out.SQL, "NOT (") {
		t.Errorf("SQL missing NOT: %s", out.SQL)
	}
}
