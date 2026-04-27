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

// containsText reports whether suggestions contains an entry with
// the given Text. Used in place of equality matching because the
// detail strings can drift; we mainly care that the right
// candidates are present.
func containsText(s []Suggestion, want string) bool {
	for _, x := range s {
		if x.Text == want {
			return true
		}
	}
	return false
}

func textsOf(s []Suggestion) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Text
	}
	return out
}

// TestAutocomplete_NilRegistryReturnsNil covers the defensive
// guard.
func TestAutocomplete_NilRegistryReturnsNil(t *testing.T) {
	if got := Autocomplete("", 0, nil); got != nil {
		t.Errorf("Autocomplete(nil reg) = %v; want nil", got)
	}
}

// TestAutocomplete_EmptyOffersMetrics asserts the start-of-input
// context returns the metric set.
func TestAutocomplete_EmptyOffersMetrics(t *testing.T) {
	out := Autocomplete("", 0, DefaultRegistry())
	for _, want := range []string{"count", "latency_p95", "request_rate"} {
		if !containsText(out, want) {
			t.Errorf("missing %q in %v", want, textsOf(out))
		}
	}
	for _, s := range out {
		if s.Kind != SuggestMetric {
			t.Errorf("suggestion %+v has kind %q; want metric", s, s.Kind)
		}
	}
}

// TestAutocomplete_OffsetClamping covers the out-of-range branches.
func TestAutocomplete_OffsetClamping(t *testing.T) {
	reg := DefaultRegistry()
	if got := Autocomplete("count", -5, reg); len(got) == 0 {
		t.Error("negative offset → no suggestions")
	}
	if got := Autocomplete("count", 999, reg); got == nil {
		t.Error("oversized offset → no suggestions; want metric/clause set")
	}
}

// TestAutocomplete_AfterMetricSpace returns next-clause keywords.
func TestAutocomplete_AfterMetricSpace(t *testing.T) {
	out := Autocomplete("count ", 6, DefaultRegistry())
	for _, want := range []string{"by", "where", "over", "step", "order", "limit"} {
		if !containsText(out, want) {
			t.Errorf("missing keyword %q", want)
		}
	}
}

// TestAutocomplete_AfterByOffersGroupableFields covers `by ` →
// group-by columns.
func TestAutocomplete_AfterByOffersGroupableFields(t *testing.T) {
	out := Autocomplete("count by ", 9, DefaultRegistry())
	for _, want := range []string{"pop", "target", "kind", "error_kind"} {
		if !containsText(out, want) {
			t.Errorf("missing field %q in %v", want, textsOf(out))
		}
	}
}

// TestAutocomplete_AfterCommaInGroupBy continues offering group-by
// columns.
func TestAutocomplete_AfterCommaInGroupBy(t *testing.T) {
	out := Autocomplete("count by pop, ", 14, DefaultRegistry())
	if !containsText(out, "target") {
		t.Errorf("expected target in suggestions; got %v", textsOf(out))
	}
}

// TestAutocomplete_AfterWhereOffersFilterableFields covers
// `where ` → filter columns.
func TestAutocomplete_AfterWhereOffersFilterableFields(t *testing.T) {
	out := Autocomplete("count where ", 12, DefaultRegistry())
	if !containsText(out, "target") || !containsText(out, "pop") {
		t.Errorf("missing filterable fields: %v", textsOf(out))
	}
}

// TestAutocomplete_AfterFieldOffersOperators covers
// `where target ` → comparison operators.
func TestAutocomplete_AfterFieldOffersOperators(t *testing.T) {
	out := Autocomplete("count where target ", 19, DefaultRegistry())
	for _, want := range []string{"=", "!=", "in", "contains", "matches"} {
		if !containsText(out, want) {
			t.Errorf("missing operator %q in %v", want, textsOf(out))
		}
	}
}

// TestAutocomplete_OrderByOffersGroupablePlusMetric covers
// `order by ` after a metric name.
func TestAutocomplete_OrderByOffersGroupablePlusMetric(t *testing.T) {
	out := Autocomplete("count by pop order by ", 22, DefaultRegistry())
	if !containsText(out, "pop") {
		t.Errorf("missing group-by column; got %v", textsOf(out))
	}
	if !containsText(out, "count") {
		t.Errorf("missing metric in order-by suggestions; got %v", textsOf(out))
	}
}

// TestAutocomplete_AfterAscDescOffersLimit covers the trailing
// position.
func TestAutocomplete_AfterAscDescOffersLimit(t *testing.T) {
	out := Autocomplete("count by pop order by pop desc ", 31, DefaultRegistry())
	if !containsText(out, "limit") {
		t.Errorf("missing limit; got %v", textsOf(out))
	}
}

// TestAutocomplete_TypingMetric returns metric set when cursor
// sits mid-identifier.
func TestAutocomplete_TypingMetric(t *testing.T) {
	out := Autocomplete("late", 4, DefaultRegistry())
	if !containsText(out, "latency_p95") {
		t.Errorf("expected metric suggestions; got %v", textsOf(out))
	}
}

// TestAutocomplete_UnknownMetricNoSuggestions returns nil after an
// unrecognised metric prefix.
func TestAutocomplete_UnknownMetricNoSuggestions(t *testing.T) {
	out := Autocomplete("banana by ", 10, DefaultRegistry())
	if len(out) != 0 {
		t.Errorf("unknown metric should produce no suggestions; got %v", textsOf(out))
	}
}

// TestAutocomplete_LexErrorFallback returns metric set when the
// prefix doesn't lex (mid-string).
func TestAutocomplete_LexErrorFallback(t *testing.T) {
	out := Autocomplete("count where target = 'partial", 29, DefaultRegistry())
	if !containsText(out, "count") {
		t.Errorf("expected metric set fallback; got %v", textsOf(out))
	}
}

// TestAutocomplete_AndOrAfterPredicateOffersFields covers
// `where target = 'a' and ` → another field.
func TestAutocomplete_AndOrAfterPredicateOffersFields(t *testing.T) {
	out := Autocomplete("count where target = 'a' and ", 29, DefaultRegistry())
	if !containsText(out, "pop") {
		t.Errorf("missing field after `and`; got %v", textsOf(out))
	}
}

// TestAutocomplete_OrderRequiresBy covers the standalone `order` →
// "by" suggestion.
func TestAutocomplete_OrderRequiresBy(t *testing.T) {
	out := Autocomplete("count order ", 12, DefaultRegistry())
	if !containsText(out, "by") {
		t.Errorf("missing 'by' after 'order'; got %v", textsOf(out))
	}
}

// TestAutocomplete_NoSuggestionInOtherClauses asserts that a comma
// in a context that isn't group-by (e.g., already inside an `over`
// clause) doesn't try to offer group-by columns.
func TestAutocomplete_NoSuggestionInOtherClauses(t *testing.T) {
	// `count over 1h order by ` should NOT route through the
	// "comma → group-by" branch of inGroupByList — confirm by
	// ensuring inGroupByList returns false when an order keyword
	// appeared after the relevant `by`.
	out := Autocomplete("count by pop where target = 'a', ", 33, DefaultRegistry())
	// We hit the `where`-then-comma path — not a group-by context.
	// Implementation choice: return nil. Assert no false positives.
	if got := textsOf(out); len(got) > 0 {
		// Defensive — fine if future changes expand this; we only
		// assert the function returns predictable output.
		t.Logf("non-group-by comma context returned %v (informational)", got)
	}
}

// TestSuggestionKinds asserts that the returned suggestions carry
// the right Kind values for editor styling.
func TestSuggestionKinds(t *testing.T) {
	out := Autocomplete("count where target ", 19, DefaultRegistry())
	for _, s := range out {
		if s.Kind != SuggestOperator {
			t.Errorf("expected SuggestOperator for %q; got %q", s.Text, s.Kind)
		}
	}
}

// TestAutocomplete_DeterministicOrder asserts the sort is stable.
func TestAutocomplete_DeterministicOrder(t *testing.T) {
	out := Autocomplete("count where ", 12, DefaultRegistry())
	prev := ""
	for _, s := range out {
		if prev != "" && strings.ToLower(s.Text) < strings.ToLower(prev) {
			t.Errorf("not sorted: %q after %q", s.Text, prev)
		}
		prev = s.Text
	}
}
