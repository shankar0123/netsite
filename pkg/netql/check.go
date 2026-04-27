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

import "fmt"

// What: the type-checker. Walks a parsed Query against a Registry
// and validates that the metric exists, the GroupBy columns are
// allowed for that metric, and every filter predicate references a
// known column with a value of the right kind.
//
// How: a single forward pass over the AST. The recursive expression
// walk reuses the same field/value matrices for `and`/`or`/`not` as
// for the top-level predicate.
//
// Why a separate Check pass (not folded into the parser): the
// parser cares about syntactic shape (`identifier op value`); the
// type-checker cares about semantic coherence (metric + column +
// value-kind compatibility). Splitting keeps each function small
// and lets us evolve the registry without touching the parser.

// Check validates the AST against the registry. Returns *TypeError
// on the first issue. Multiple-error aggregation is a Phase 1 add
// once the UI surface knows what to do with a list.
func Check(q *Query, reg *Registry) error {
	if q == nil {
		return &TypeError{Reason: "nil query"}
	}
	if reg == nil {
		return &TypeError{Reason: "nil registry"}
	}
	spec, ok := reg.Get(q.Metric)
	if !ok {
		return &TypeError{Reason: fmt.Sprintf("unknown metric %q", q.Metric)}
	}
	for _, gb := range q.GroupBy {
		if _, ok := spec.GroupBy[gb]; !ok {
			return &TypeError{Reason: fmt.Sprintf("metric %q cannot be grouped by %q", q.Metric, gb)}
		}
	}
	if q.Filter != nil {
		if err := checkExpr(q.Filter, spec); err != nil {
			return err
		}
	}
	if q.OrderBy != nil {
		// Order column must be either the metric itself or one of
		// the user's selected group-by columns. Operators expect to
		// sort by what's in the SELECT list; ordering by a column
		// that isn't projected would force ClickHouse to either
		// surface a confusing alias-resolution failure or silently
		// reach into ungrouped data.
		ok := q.OrderBy.Field == q.Metric
		if !ok {
			for _, gb := range q.GroupBy {
				if gb == q.OrderBy.Field {
					ok = true
					break
				}
			}
		}
		if !ok {
			return &TypeError{Reason: fmt.Sprintf("order-by field %q is not the metric or a selected group-by column", q.OrderBy.Field)}
		}
	}
	if q.Limit != nil && *q.Limit <= 0 {
		return &TypeError{Reason: "limit must be > 0"}
	}
	return nil
}

func checkExpr(e *Expr, spec *MetricSpec) error {
	switch {
	case e.Predicate != nil:
		return checkPredicate(e.Predicate, spec)
	case len(e.And) > 0:
		for _, t := range e.And {
			if err := checkExpr(t, spec); err != nil {
				return err
			}
		}
	case len(e.Or) > 0:
		for _, t := range e.Or {
			if err := checkExpr(t, spec); err != nil {
				return err
			}
		}
	case e.Not != nil:
		return checkExpr(e.Not, spec)
	}
	return nil
}

func checkPredicate(p *Predicate, spec *MetricSpec) error {
	field, ok := spec.Filter[p.Field]
	if !ok {
		return &TypeError{Reason: fmt.Sprintf("metric %q cannot be filtered by %q", spec.Name, p.Field)}
	}
	if !opCompatible(p.Op, field.Kind, p.Value.Kind) {
		return &TypeError{Reason: fmt.Sprintf("operator %q is not valid for field %q (%s) with %s value",
			p.Op, p.Field, fieldKindName(field.Kind), valueKindName(p.Value.Kind))}
	}
	return nil
}

// opCompatible enforces the operator/field/value matrix:
//   - String fields accept =/!= against scalar string;
//     in/contains/matches against string list (`in`) or scalar string.
//   - Numeric / duration fields accept =/!=/</<=/>/>= against scalar
//     number; in against number list.
//
// Booleans (and any future fields) extend this matrix.
func opCompatible(op Op, field FieldKind, val ValueKind) bool {
	switch field {
	case FieldString:
		switch op {
		case OpEq, OpNe:
			return val == ValString
		case OpIn:
			return val == ValStringList
		case OpContains, OpMatches:
			return val == ValString
		}
	case FieldNumber, FieldDuration:
		switch op {
		case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
			return val == ValNumber
		case OpIn:
			return val == ValNumberList
		}
	}
	return false
}

func fieldKindName(k FieldKind) string {
	switch k {
	case FieldString:
		return "string"
	case FieldNumber:
		return "number"
	case FieldDuration:
		return "duration"
	default:
		return "unknown"
	}
}

func valueKindName(k ValueKind) string {
	switch k {
	case ValString:
		return "string"
	case ValNumber:
		return "number"
	case ValStringList:
		return "string list"
	case ValNumberList:
		return "number list"
	default:
		return "unknown"
	}
}
