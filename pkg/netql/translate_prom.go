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
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// What: the PromQL translator. Same AST as the ClickHouse
// translator; second visitor. Compiles a netql Query into a single
// PromQL expression string.
//
// How: deterministic walk. Predicates project into the metric's
// label set with `key="value"` pairs (ANDed together — PromQL's
// label matcher syntax is conjunctive by default). The Prom shape
// declared in MetricSpec.Prom decides whether the outer wrapper is
// `sum by (...)(rate(...))` (counter) or
// `histogram_quantile(q, sum by (le, ...) (rate(..._bucket[...])))`.
//
// Why a separate translator and not a generic SQL emitter:
// PromQL's surface is shaped fundamentally differently from SQL —
// label matchers live inside the metric selector, range vectors
// have their own syntax, aggregation and rate compose by stacking,
// not by clauses. Trying to share a code path across the two
// produces a Frankenstein. Two independent visitors keep each
// translator simple and easy to read.
//
// Why no parameter binding: PromQL has no parameter mechanism.
// Label values become quoted strings inline. The translator
// rejects predicate operators that PromQL cannot express
// (numeric comparisons against a label, OR, NOT) — they surface as
// *TranslateError so the operator gets a clear message in the UI.

const (
	// defaultPromRange applies when the user omits `over`. PromQL
	// queries against rate() always need a range vector; we pick 5m
	// to match the prom mixins / Datadog defaults that operators are
	// already used to.
	defaultPromRange = "5m"
)

// TranslatePrometheus compiles q into a PromQL expression.
// Returns *TranslateError when the metric is not Prometheus-backed
// or when the filter expression cannot be expressed as a PromQL
// label matcher.
//
// Note: PromQL has no concept of "tenant scoping" via parameter
// binding; the caller is responsible for adding `tenant_id="X"` if
// their metric is multi-tenant. v0.0.10's Prometheus metrics are
// scraped from the (single-tenant) control plane, so we don't
// inject tenant scoping here. This is documented in the algorithm
// doc and revisited when Phase 2's per-tenant exporters land.
func TranslatePrometheus(q *Query, reg *Registry) (string, error) {
	if err := Check(q, reg); err != nil {
		return "", err
	}
	spec, _ := reg.Get(q.Metric)
	if spec.Backend != BackendPrometheus {
		return "", &TranslateError{
			Reason: fmt.Sprintf("metric %q is in backend %q, not prometheus", q.Metric, spec.Backend),
		}
	}
	if spec.Prom == nil {
		return "", &TranslateError{Reason: fmt.Sprintf("metric %q has no Prometheus spec", q.Metric)}
	}

	t := &promTranslator{spec: spec}
	if err := t.collectLabels(q.Filter); err != nil {
		return "", err
	}
	t.byClause = buildByClause(q, spec)
	t.rangeStr = buildRange(q.Over)

	return t.emit(spec.Prom), nil
}

// promTranslator carries the in-progress label matcher set + the
// emitted strings used by the templating step.
type promTranslator struct {
	spec     *MetricSpec
	matchers []string
	byClause string
	rangeStr string
}

// collectLabels walks the filter expression and builds a flat list
// of `label="value"` matcher strings. PromQL's label-matcher syntax
// is implicitly AND, so we only support an AND-shaped expression
// (or a single predicate, or no filter at all). Anything else is a
// TranslateError so the operator sees "PromQL can't express NOT/OR
// in label matchers" rather than getting bad-but-syntactically-valid
// PromQL.
func (t *promTranslator) collectLabels(e *Expr) error {
	if e == nil {
		return nil
	}
	switch {
	case e.Predicate != nil:
		return t.collectPredicate(e.Predicate)
	case len(e.And) > 0:
		for _, term := range e.And {
			if err := t.collectLabels(term); err != nil {
				return err
			}
		}
		return nil
	case len(e.Or) > 0:
		return &TranslateError{
			Reason: "PromQL label matchers cannot express OR; rewrite as separate queries or use `=~` regex",
		}
	case e.Not != nil:
		return &TranslateError{
			Reason: "PromQL label matchers cannot express top-level NOT; use != or !~ on the inner predicate instead",
		}
	}
	return nil
}

// collectPredicate maps one netql predicate to a PromQL label
// matcher. PromQL supports four operators on labels: `=`, `!=`,
// `=~` (regex match), `!~` (regex non-match). Numeric comparisons
// (<, <=, >, >=) and `in` (a list) are not expressible against
// labels — PromQL does numeric comparison at the value level, not
// the label level — so we reject them at translation.
func (t *promTranslator) collectPredicate(p *Predicate) error {
	label, ok := t.spec.GroupBy[p.Field]
	if !ok {
		// Filter map covers GroupBy + a few extras for canary
		// results, but Prometheus labels are typically a subset; we
		// re-check against the GroupBy → Prometheus label name
		// mapping for safety.
		if fs, ok := t.spec.Filter[p.Field]; ok {
			label = fs.Column
		} else {
			return &TranslateError{Reason: fmt.Sprintf("unknown label %q", p.Field)}
		}
	}
	switch p.Op {
	case OpEq:
		t.matchers = append(t.matchers, fmt.Sprintf(`%s=%s`, label, quote(p.Value.String)))
	case OpNe:
		t.matchers = append(t.matchers, fmt.Sprintf(`%s!=%s`, label, quote(p.Value.String)))
	case OpMatches:
		t.matchers = append(t.matchers, fmt.Sprintf(`%s=~%s`, label, quote(p.Value.String)))
	case OpContains:
		// PromQL has no contains; lower contains→regex with the
		// substring escaped.
		t.matchers = append(t.matchers, fmt.Sprintf(`%s=~%s`, label, quote(".*"+regexEscape(p.Value.String)+".*")))
	default:
		return &TranslateError{
			Reason: fmt.Sprintf("operator %q is not expressible as a PromQL label matcher (use =, !=, contains, matches)", p.Op),
		}
	}
	return nil
}

// emit produces the final expression string, switching on whether
// the metric is a histogram quantile (Quantile > 0) or a
// counter/gauge.
func (t *promTranslator) emit(p *PromSpec) string {
	matcherStr := ""
	if len(t.matchers) > 0 {
		// Sort the matcher list deterministically so snapshot tests
		// remain stable against map-iteration order changes upstream.
		sort.Strings(t.matchers)
		matcherStr = "{" + strings.Join(t.matchers, ",") + "}"
	}

	if p.Quantile > 0 {
		// histogram_quantile shape:
		//   histogram_quantile(<q>,
		//     sum by (le[, group_by_cols]) (rate(<bucket>{<matchers>}[<range>])))
		byLE := "le"
		if t.byClause != "" {
			// The byClause is `by (a, b)`; widen to `by (le, a, b)`.
			inner := strings.TrimPrefix(t.byClause, "by (")
			inner = strings.TrimSuffix(inner, ")")
			byLE = "le, " + inner
		}
		fnInner := fmt.Sprintf("%s(%s%s[%s])", p.Function, p.MetricName, matcherStr, t.rangeStr)
		aggInner := fmt.Sprintf("%s by (%s) (%s)", p.Aggregator, byLE, fnInner)
		q := strconv.FormatFloat(p.Quantile, 'f', -1, 64)
		return fmt.Sprintf("histogram_quantile(%s, %s)", q, aggInner)
	}

	// Counter/gauge shape:
	//   <agg>[ by (...)] ( <fn>(<metric>{<matchers>}[<range>]) )
	// or, for raw gauges (Function == ""):
	//   <agg>[ by (...)] ( <metric>{<matchers>} )
	var inner string
	if p.Function != "" {
		inner = fmt.Sprintf("%s(%s%s[%s])", p.Function, p.MetricName, matcherStr, t.rangeStr)
	} else {
		inner = fmt.Sprintf("%s%s", p.MetricName, matcherStr)
	}
	if t.byClause == "" {
		return fmt.Sprintf("%s(%s)", p.Aggregator, inner)
	}
	// Spaces around the by-clause produce idiomatic PromQL —
	// `sum by (route) (rate(...))`.
	return fmt.Sprintf("%s %s (%s)", p.Aggregator, t.byClause, inner)
}

// buildByClause produces "by (label1, label2)" — or empty string
// when no GroupBy is set. PromQL by-clauses go between the
// aggregator and its argument: `sum by (route) (rate(...))`.
func buildByClause(q *Query, spec *MetricSpec) string {
	if len(q.GroupBy) == 0 {
		return ""
	}
	cols := make([]string, 0, len(q.GroupBy))
	for _, gb := range q.GroupBy {
		// Use the underlying label name from the spec, not the
		// netql identifier — PromQL works in the metric's native
		// label namespace.
		if v, ok := spec.GroupBy[gb]; ok {
			cols = append(cols, v)
		} else {
			cols = append(cols, gb)
		}
	}
	return "by (" + strings.Join(cols, ", ") + ")"
}

// buildRange returns the PromQL range string ("5m", "24h", …) from
// the netql Duration AST. Falls back to defaultPromRange when no
// `over` clause was given.
func buildRange(d *Duration) string {
	if d == nil {
		return defaultPromRange
	}
	return fmt.Sprintf("%d%s", d.Count, d.Unit)
}

// quote turns a string into a PromQL-safe quoted label value. We
// escape backslash and double-quote; everything else is fine.
func quote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '"' {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	sb.WriteByte('"')
	return sb.String()
}

// regexEscape escapes the eight characters with regex meaning so
// `contains 'foo.bar'` becomes `=~".*foo\.bar.*"`. RE2 dialect.
func regexEscape(s string) string {
	const meta = `\.+*?()|[]{}^$`
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.ContainsRune(meta, rune(c)) {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}
