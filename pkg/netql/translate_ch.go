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
	"strings"
)

// What: the ClickHouse translator. Takes a type-checked Query and
// the calling tenant, and emits a parameterised SQL string +
// argument list.
//
// How: deterministic AST walk producing fragment strings into a
// strings.Builder. Every user-supplied scalar (predicate string,
// number) goes into args; only the structural keywords and column
// names land in the SQL text. The translator preserves group-by
// order from the user's query so dashboards stay stable.
//
// Why this shape: parameterised SQL means we never string-format
// untrusted input. Tenant scoping is injected as the first
// parameter ($1) so it's impossible to forget. The deterministic
// output is what makes corpus snapshot tests work — same query in,
// same SQL out.

const (
	// defaultLimit caps result rows when the user omits `limit`. Set
	// high enough to render any human-readable table; low enough
	// that a runaway query can't memory-bomb the API or the UI.
	defaultLimit = 10000
	// defaultOver applies when the user omits `over`. One hour of
	// canary data covers one full SLO eval cycle — the most common
	// "what's happening right now" question.
	defaultOverHours = 1
)

// Translation is the structured output of TranslateClickHouse:
// the SQL string, the positional argument values, and a quick
// echo of which metric drove the query so callers can route to
// the right ClickHouse pool.
type Translation struct {
	SQL    string
	Args   []any
	Metric *MetricSpec
}

// TranslateClickHouse compiles q into ClickHouse SQL. tenantID is
// the caller's tenant; it is bound as the first positional arg and
// always appears in the WHERE clause.
//
// Returns *TranslateError when the metric exists in the registry
// but is not ClickHouse-backed.
func TranslateClickHouse(q *Query, reg *Registry, tenantID string) (*Translation, error) {
	if err := Check(q, reg); err != nil {
		return nil, err
	}
	spec, _ := reg.Get(q.Metric)
	if spec.Backend != BackendClickHouse {
		return nil, &TranslateError{
			Reason: fmt.Sprintf("metric %q is in backend %q, not clickhouse", q.Metric, spec.Backend),
		}
	}

	t := &chTranslator{spec: spec}
	t.args = append(t.args, tenantID) // $1 always tenant_id

	t.writeSelect(q)
	t.writeFrom()
	t.writeWhere(q, tenantID)
	t.writeGroupBy(q)
	t.writeOrderBy(q)
	t.writeLimit(q)

	return &Translation{
		SQL:    t.sb.String(),
		Args:   t.args,
		Metric: spec,
	}, nil
}

// chTranslator carries the in-progress SQL text and bound arg list.
// One instance per call; not safe for reuse.
type chTranslator struct {
	sb   strings.Builder
	args []any
	spec *MetricSpec
}

func (t *chTranslator) writeSelect(q *Query) {
	t.sb.WriteString("SELECT\n")
	// Group-by columns come first, aliased to the netql identifier.
	// Stable order = the user's order — operators expect their query
	// shape to be reflected in the result column order.
	for _, gb := range q.GroupBy {
		col := t.spec.GroupBy[gb]
		fmt.Fprintf(&t.sb, "  %s AS %s,\n", col, gb)
	}
	// Metric expression last, aliased to the metric name.
	fmt.Fprintf(&t.sb, "  %s AS %s\n", t.spec.Selector, t.spec.Name)
}

func (t *chTranslator) writeFrom() {
	fmt.Fprintf(&t.sb, "FROM %s\n", t.spec.Source)
}

func (t *chTranslator) writeWhere(q *Query, _ string) {
	t.sb.WriteString("WHERE tenant_id = $1\n")

	// `over` is a time-range filter on observed_at. We always add
	// one — either the user-supplied one or the default 1h.
	count, unit := defaultOverHours, "h"
	if q.Over != nil {
		count, unit = q.Over.Count, q.Over.Unit
	}
	fmt.Fprintf(&t.sb, "  AND observed_at >= now() - INTERVAL %d %s\n", count, unitToCH(unit))

	if q.Filter != nil {
		t.sb.WriteString("  AND ")
		t.writeExpr(q.Filter)
		t.sb.WriteString("\n")
	}
}

func (t *chTranslator) writeGroupBy(q *Query) {
	if len(q.GroupBy) == 0 {
		return
	}
	t.sb.WriteString("GROUP BY ")
	t.sb.WriteString(strings.Join(q.GroupBy, ", "))
	t.sb.WriteString("\n")
}

func (t *chTranslator) writeOrderBy(q *Query) {
	if q.OrderBy == nil {
		// Default: order by the first group-by column ASC, if any,
		// otherwise no ORDER BY. Predictable output for table
		// rendering without forcing every operator to type it.
		if len(q.GroupBy) > 0 {
			fmt.Fprintf(&t.sb, "ORDER BY %s ASC\n", q.GroupBy[0])
		}
		return
	}
	dir := strings.ToUpper(string(q.OrderBy.Direction))
	if dir == "" {
		dir = "ASC"
	}
	fmt.Fprintf(&t.sb, "ORDER BY %s %s\n", q.OrderBy.Field, dir)
}

func (t *chTranslator) writeLimit(q *Query) {
	limit := defaultLimit
	if q.Limit != nil {
		limit = *q.Limit
	}
	fmt.Fprintf(&t.sb, "LIMIT %d", limit)
}

// writeExpr walks the boolean tree, parameterising every user value.
// Operator precedence is already baked into the AST shape (Or wraps
// And wraps Not wraps Predicate); we only need to bracket each
// composite to make the SQL unambiguous.
func (t *chTranslator) writeExpr(e *Expr) {
	switch {
	case e.Predicate != nil:
		t.writePredicate(e.Predicate)
	case len(e.And) > 0:
		t.writeJoin(" AND ", e.And)
	case len(e.Or) > 0:
		t.writeJoin(" OR ", e.Or)
	case e.Not != nil:
		t.sb.WriteString("NOT (")
		t.writeExpr(e.Not)
		t.sb.WriteString(")")
	}
}

func (t *chTranslator) writeJoin(sep string, parts []*Expr) {
	t.sb.WriteString("(")
	for i, p := range parts {
		if i > 0 {
			t.sb.WriteString(sep)
		}
		t.writeExpr(p)
	}
	t.sb.WriteString(")")
}

func (t *chTranslator) writePredicate(p *Predicate) {
	col := t.spec.Filter[p.Field].Column
	switch p.Op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		t.bindScalar(col, string(p.Op), p.Value)
	case OpIn:
		t.bindList(col, p.Value)
	case OpContains:
		// `contains` → ClickHouse `position(<col>, <needle>) > 0`.
		// We use position rather than `LIKE '%x%'` because position
		// doesn't require us to escape `%` and `_` in the user's
		// string.
		t.args = append(t.args, p.Value.String)
		fmt.Fprintf(&t.sb, "position(%s, $%d) > 0", col, len(t.args))
	case OpMatches:
		// `matches` → ClickHouse `match(<col>, <pattern>)` (RE2).
		t.args = append(t.args, p.Value.String)
		fmt.Fprintf(&t.sb, "match(%s, $%d)", col, len(t.args))
	}
}

func (t *chTranslator) bindScalar(col, op string, v Value) {
	switch v.Kind {
	case ValString:
		t.args = append(t.args, v.String)
	case ValNumber:
		t.args = append(t.args, v.Number)
	}
	fmt.Fprintf(&t.sb, "%s %s $%d", col, op, len(t.args))
}

func (t *chTranslator) bindList(col string, v Value) {
	switch v.Kind {
	case ValStringList:
		// Bind each list element as its own positional arg. ClickHouse
		// supports `IN (?, ?, ?)` shape; alternatively `IN ?` with a
		// tuple, but per-element binding maps cleanly to standard
		// driver semantics.
		placeholders := make([]string, len(v.Strings))
		for i, s := range v.Strings {
			t.args = append(t.args, s)
			placeholders[i] = fmt.Sprintf("$%d", len(t.args))
		}
		// Sort deterministically inside the IN list so snapshot
		// tests remain stable across map-iteration order changes
		// in the parser.
		sort.Strings(placeholders) // stable across Go versions
		fmt.Fprintf(&t.sb, "%s IN (%s)", col, strings.Join(placeholders, ", "))
	case ValNumberList:
		placeholders := make([]string, len(v.Numbers))
		for i, n := range v.Numbers {
			t.args = append(t.args, n)
			placeholders[i] = fmt.Sprintf("$%d", len(t.args))
		}
		sort.Strings(placeholders)
		fmt.Fprintf(&t.sb, "%s IN (%s)", col, strings.Join(placeholders, ", "))
	}
}

// unitToCH maps the netql duration unit to the ClickHouse INTERVAL
// keyword. We deliberately don't pass `m` straight through — `m`
// in netql means "minute" but `m` in ClickHouse INTERVAL would be
// ambiguous (some dialects use it for "month"). Spelling it out
// removes the ambiguity.
func unitToCH(unit string) string {
	switch unit {
	case "s":
		return "SECOND"
	case "m":
		return "MINUTE"
	case "h":
		return "HOUR"
	case "d":
		return "DAY"
	case "w":
		return "WEEK"
	default:
		// Should be unreachable — the lexer only emits known units.
		return strings.ToUpper(unit)
	}
}
