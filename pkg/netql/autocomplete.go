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
	"sort"
	"strings"
)

// What: a context-sensitive autocomplete. Given an input string and
// a byte offset (the cursor position), returns a sorted list of
// candidate completions appropriate for the current grammatical
// context.
//
// How: re-tokenise the prefix up to the cursor; look at the trailing
// token's kind and the immediately-preceding token to decide which
// branch of the grammar the cursor is in. The contexts we cover in
// v0.0.10:
//
//   - empty / start of input → metric names from the registry
//   - directly after a metric → next-clause keywords (by, where, …)
//   - after `by` or after `,` in a group-by list → spec.GroupBy keys
//   - after `where` or after a boolean keyword → spec.Filter keys
//   - after a filter identifier → operator tokens
//   - after `order` → "by"
//   - after `order by` → spec.GroupBy keys + the metric name
//   - directly after `asc`/`desc` → "limit"
//
// Why context-sensitive rather than fuzzy-prefix: an editor that
// suggests every possible token at every position is hostile —
// operators get a wall of irrelevant candidates and the experience
// degrades to "type the whole thing yourself." A grammar-aware
// completer narrows the set to what the parser would actually
// accept next.
//
// What we deliberately don't do (yet): completion of string
// literals (e.g., the right-hand side of `pop = '<cursor>'`).
// That requires either querying ClickHouse for distinct values or
// shipping a per-tenant suggestion cache; both arrive when the
// React shell binds the autocomplete (Phase 0 task 0.25).

// Suggestion is one autocomplete candidate.
type Suggestion struct {
	// Text is the literal string that would be inserted into the
	// editor.
	Text string
	// Detail is a short human-readable hint shown next to the
	// suggestion in the UI (the metric description, the keyword's
	// purpose, the field's type, etc.). May be empty.
	Detail string
	// Kind classifies the suggestion for icon/styling decisions.
	Kind SuggestionKind
}

// SuggestionKind enumerates the rough class of a suggestion. The
// editor uses this to pick an icon (metric, keyword, identifier).
type SuggestionKind string

// Canonical SuggestionKind values.
const (
	SuggestMetric   SuggestionKind = "metric"
	SuggestKeyword  SuggestionKind = "keyword"
	SuggestField    SuggestionKind = "field"
	SuggestOperator SuggestionKind = "operator"
)

// Autocomplete returns suggestions appropriate at the byte offset.
// offset must satisfy 0 ≤ offset ≤ len(input); out-of-range offsets
// are clamped.
//
// The returned slice is alphabetically sorted (by Text) so the UI
// doesn't have to. Same-prefix filtering is the editor's job — we
// return all candidates that fit the *grammatical* context.
func Autocomplete(input string, offset int, reg *Registry) []Suggestion {
	if reg == nil {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(input) {
		offset = len(input)
	}
	prefix := input[:offset]

	// Re-tokenise the prefix. Lex errors are fine — they happen
	// midway through typing (e.g., `where target = 'partial`); we
	// fall back to "metric context" suggestions because we don't
	// know better.
	toks, err := Lex(prefix)
	if err != nil {
		return metricSuggestions(reg)
	}
	// Drop the trailing TokEOF.
	if n := len(toks); n > 0 && toks[n-1].Kind == TokEOF {
		toks = toks[:n-1]
	}

	// 1. Empty input or whitespace-only → metric context.
	if len(toks) == 0 {
		return metricSuggestions(reg)
	}

	// 2. After exactly one identifier with no trailing whitespace,
	// the user is likely still typing the metric name — return
	// metric suggestions filtered by prefix is the editor's job; we
	// return the full set.
	last := toks[len(toks)-1]
	if len(toks) == 1 && last.Kind == TokIdent {
		// If the cursor sits immediately after the last byte (no
		// trailing space), assume mid-typing → metric context.
		if !endsWithSpace(prefix) {
			return metricSuggestions(reg)
		}
		// Otherwise, the metric is fully typed and we offer next-
		// clause keywords.
		spec, _ := reg.Get(last.Value)
		return clauseKeywordsAfterMetric(spec)
	}

	// 3. We have ≥2 tokens. Find the resolved metric (first ident).
	if toks[0].Kind != TokIdent {
		return nil
	}
	spec, ok := reg.Get(toks[0].Value)
	if !ok {
		// Unknown metric → no useful suggestions until they fix it.
		return nil
	}

	// Look at the trailing token to decide. We also peek one back
	// for some contexts (e.g., after `,` in a group-by list).
	prev := Token{}
	if len(toks) >= 2 {
		prev = toks[len(toks)-2]
	}

	switch {
	case last.Kind == TokKeyword && last.Value == "by":
		// `... by ` → group-by columns.
		// `order by ` → group-by columns + metric name.
		if isOrderByContext(toks) {
			return groupableFieldsPlusMetric(spec)
		}
		return groupableFields(spec)

	case last.Kind == TokKeyword && last.Value == "where":
		return filterableFields(spec)

	case last.Kind == TokComma:
		// Inside a group-by list (preceded by `by` somewhere in the
		// recent past) → group-by columns. Inside an IN list → no
		// suggestions (string literals).
		if inGroupByList(toks) {
			return groupableFields(spec)
		}
		return nil

	case last.Kind == TokIdent && prev.Kind == TokKeyword && prev.Value == "where":
		return operatorSuggestions()

	case last.Kind == TokIdent && (prev.Kind == TokKeyword && (prev.Value == "and" || prev.Value == "or" || prev.Value == "not")):
		return operatorSuggestions()

	case last.Kind == TokKeyword && (last.Value == "and" || last.Value == "or"):
		return filterableFields(spec)

	case last.Kind == TokKeyword && last.Value == "order":
		return []Suggestion{{Text: "by", Kind: SuggestKeyword, Detail: "order by"}}

	case last.Kind == TokKeyword && (last.Value == "asc" || last.Value == "desc"):
		return []Suggestion{{Text: "limit", Kind: SuggestKeyword, Detail: "cap result row count"}}
	}

	return nil
}

// endsWithSpace reports whether s's last byte is whitespace. Used
// to disambiguate "still typing the metric" from "metric finished,
// what's next."
func endsWithSpace(s string) bool {
	if s == "" {
		return false
	}
	c := s[len(s)-1]
	return c == ' ' || c == '\t' || c == '\n'
}

// metricSuggestions returns one entry per registered metric.
func metricSuggestions(reg *Registry) []Suggestion {
	out := make([]Suggestion, 0, len(reg.metrics))
	for _, m := range reg.metrics {
		out = append(out, Suggestion{
			Text:   m.Name,
			Detail: m.Description,
			Kind:   SuggestMetric,
		})
	}
	sortByText(out)
	return out
}

// clauseKeywordsAfterMetric returns the keywords that can legally
// follow the metric per the EBNF.
func clauseKeywordsAfterMetric(spec *MetricSpec) []Suggestion {
	out := []Suggestion{
		{Text: "by", Kind: SuggestKeyword, Detail: "group by columns"},
		{Text: "where", Kind: SuggestKeyword, Detail: "filter predicate"},
		{Text: "over", Kind: SuggestKeyword, Detail: "time range, e.g., 24h"},
		{Text: "step", Kind: SuggestKeyword, Detail: "step duration, e.g., 5m"},
		{Text: "order", Kind: SuggestKeyword, Detail: "order results"},
		{Text: "limit", Kind: SuggestKeyword, Detail: "cap row count"},
	}
	_ = spec
	sortByText(out)
	return out
}

func groupableFields(spec *MetricSpec) []Suggestion {
	out := make([]Suggestion, 0, len(spec.GroupBy))
	for k := range spec.GroupBy {
		out = append(out, Suggestion{Text: k, Detail: "groupable column", Kind: SuggestField})
	}
	sortByText(out)
	return out
}

func groupableFieldsPlusMetric(spec *MetricSpec) []Suggestion {
	out := groupableFields(spec)
	out = append(out, Suggestion{Text: spec.Name, Detail: "the metric value", Kind: SuggestMetric})
	sortByText(out)
	return out
}

func filterableFields(spec *MetricSpec) []Suggestion {
	out := make([]Suggestion, 0, len(spec.Filter))
	for k, fs := range spec.Filter {
		out = append(out, Suggestion{
			Text:   k,
			Detail: "filterable " + fieldKindName(fs.Kind),
			Kind:   SuggestField,
		})
	}
	sortByText(out)
	return out
}

func operatorSuggestions() []Suggestion {
	out := []Suggestion{
		{Text: "=", Kind: SuggestOperator, Detail: "equals"},
		{Text: "!=", Kind: SuggestOperator, Detail: "not equals"},
		{Text: "<", Kind: SuggestOperator, Detail: "less than"},
		{Text: "<=", Kind: SuggestOperator, Detail: "less than or equal"},
		{Text: ">", Kind: SuggestOperator, Detail: "greater than"},
		{Text: ">=", Kind: SuggestOperator, Detail: "greater than or equal"},
		{Text: "in", Kind: SuggestOperator, Detail: "in list, e.g., in ('a','b')"},
		{Text: "contains", Kind: SuggestOperator, Detail: "substring match"},
		{Text: "matches", Kind: SuggestOperator, Detail: "RE2 regex match"},
	}
	sortByText(out)
	return out
}

// isOrderByContext reports whether the most recent grammatical
// position is `order by` (the `by` keyword preceded by `order`).
func isOrderByContext(toks []Token) bool {
	if len(toks) < 2 {
		return false
	}
	last := toks[len(toks)-1]
	prev := toks[len(toks)-2]
	return last.Kind == TokKeyword && last.Value == "by" &&
		prev.Kind == TokKeyword && prev.Value == "order"
}

// inGroupByList walks back through the token list to detect whether
// the most recent `by` was a group-by `by` (not `order by`). We look
// for a `by` keyword that is NOT preceded by `order`.
func inGroupByList(toks []Token) bool {
	for i := len(toks) - 1; i >= 0; i-- {
		t := toks[i]
		if t.Kind == TokKeyword {
			switch t.Value {
			case "where", "over", "step", "order", "limit":
				// We've crossed into another clause; the most
				// recent group-by ended.
				return false
			case "by":
				// `by` directly preceded by `order` is not a group-by.
				if i > 0 {
					prev := toks[i-1]
					if prev.Kind == TokKeyword && prev.Value == "order" {
						return false
					}
				}
				return true
			}
		}
	}
	return false
}

// sortByText is a tiny helper that keeps the output deterministic
// for snapshot tests and for predictable UI ordering.
func sortByText(s []Suggestion) {
	sort.Slice(s, func(i, j int) bool {
		// Case-insensitive primary key, with case-sensitive tie-break
		// so distinct same-letter entries (rare in netql) still sort
		// stably.
		ai, bi := strings.ToLower(s[i].Text), strings.ToLower(s[j].Text)
		if ai != bi {
			return ai < bi
		}
		return s[i].Text < s[j].Text
	})
}
