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

// What: the metric registry — the catalogue of metrics netql knows
// how to translate. Each entry pins down which backend the metric
// lives in, which columns are groupable, which columns are
// filterable (and the value type for each filter), and the SQL/PromQL
// fragment that computes the metric.
//
// How: a hand-curated map keyed by metric name. The type-checker
// looks up each Query's Metric here and validates GroupBy and
// Filter against the entry's allow-lists before the translator
// emits SQL.
//
// Why a registry rather than free-form column references: netql
// users should never need to know the underlying column names. The
// registry is the indirection layer that makes "latency_p95"
// translate to `quantile(0.95)(latency_ms)`. It also makes tenant
// scoping enforceable — the registry decides what's filterable, so
// `tenant_id` is simply not in the allow-list.

// Backend identifies which data store is the metric's home.
type Backend string

// Canonical Backend values.
const (
	BackendClickHouse Backend = "clickhouse"
	BackendPrometheus Backend = "prometheus" // reserved for v0.0.10
)

// FieldKind enumerates the value type of a filterable column. The
// type-checker uses this to reject "string-only column compared
// against a number" and similar mistakes.
type FieldKind int

// Canonical FieldKind values.
const (
	FieldString FieldKind = iota
	FieldNumber
	FieldDuration
)

// FieldSpec is the type contract for one filterable column.
type FieldSpec struct {
	Kind FieldKind
	// Column is the underlying ClickHouse column name. For
	// PromQL-backed metrics it would be the label name.
	Column string
}

// MetricSpec is the registry entry for one metric.
type MetricSpec struct {
	Name        string
	Backend     Backend
	Description string
	// Selector is the SQL expression that computes the metric value
	// in ClickHouse (e.g., `quantile(0.95)(latency_ms)`). The
	// translator wraps this in `<selector> AS <metric_name>`.
	Selector string
	// Source is the underlying table for ClickHouse-backed metrics.
	// All v0.0.9 metrics share `canary_results`; later metrics can
	// point at `bgp_updates`, `flow_records`, etc.
	Source string
	// GroupBy maps netql group identifiers (e.g., "pop") to the
	// underlying column name (e.g., "pop_id"). The translator
	// emits `<column> AS <netql_id>`.
	GroupBy map[string]string
	// Filter maps netql filter identifiers to FieldSpecs. Identifiers
	// not in this map are rejected by the type-checker.
	Filter map[string]FieldSpec
}

// Registry is the lookup container for MetricSpec. We keep it as a
// pointer-receiver type so callers can extend it at runtime if a
// future deployment wants to register custom metrics (per-tenant
// catalog plugins are a Phase 2 idea; the registry is already
// shape-ready).
type Registry struct {
	metrics map[string]*MetricSpec
}

// NewRegistry returns an empty Registry. Use DefaultRegistry for
// the v0.0.9 baseline.
func NewRegistry() *Registry {
	return &Registry{metrics: map[string]*MetricSpec{}}
}

// Register adds a MetricSpec. Re-registering the same name overrides
// the existing entry (last write wins). Returns the registry for
// chaining.
func (r *Registry) Register(m *MetricSpec) *Registry {
	r.metrics[m.Name] = m
	return r
}

// Get looks up a metric by name. Returns nil + false when absent.
func (r *Registry) Get(name string) (*MetricSpec, bool) {
	m, ok := r.metrics[name]
	return m, ok
}

// Names returns all registered metric names. Order is
// implementation-defined; callers who want deterministic order
// should sort.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.metrics))
	for n := range r.metrics {
		out = append(out, n)
	}
	return out
}

// DefaultRegistry returns the v0.0.9 baseline registry: three
// metrics over `canary_results`, all in the ClickHouse backend.
//
// Rationale for this set: success_rate + latency_p95 + count cover
// the three views an operator wants when looking at a synthetic
// monitoring dashboard. Adding more requires a pure data change
// (new MetricSpec) — the parser, type-checker, and translator stay
// untouched.
func DefaultRegistry() *Registry {
	canaryGroupBy := map[string]string{
		"pop":        "pop_id",
		"target":     "target",
		"kind":       "kind",
		"error_kind": "error_kind",
	}
	canaryFilter := map[string]FieldSpec{
		"pop":         {Kind: FieldString, Column: "pop_id"},
		"target":      {Kind: FieldString, Column: "target"},
		"kind":        {Kind: FieldString, Column: "kind"},
		"error_kind":  {Kind: FieldString, Column: "error_kind"},
		"observed_at": {Kind: FieldDuration, Column: "observed_at"},
	}

	r := NewRegistry()
	r.Register(&MetricSpec{
		Name:        "success_rate",
		Backend:     BackendClickHouse,
		Description: "Fraction of canary results without an error_kind.",
		Selector:    "countIf(error_kind = '') / count(*)",
		Source:      "canary_results",
		GroupBy:     canaryGroupBy,
		Filter:      canaryFilter,
	})
	r.Register(&MetricSpec{
		Name:        "latency_p95",
		Backend:     BackendClickHouse,
		Description: "95th-percentile request latency in milliseconds.",
		Selector:    "quantile(0.95)(latency_ms)",
		Source:      "canary_results",
		// Latency p95 is meaningless to break down by error_kind
		// (failed canaries have no latency to speak of) — restrict
		// the group-by set rather than letting the translator emit
		// nonsense.
		GroupBy: map[string]string{
			"pop":    "pop_id",
			"target": "target",
			"kind":   "kind",
		},
		Filter: canaryFilter,
	})
	r.Register(&MetricSpec{
		Name:        "count",
		Backend:     BackendClickHouse,
		Description: "Number of canary observations matching the filter.",
		Selector:    "count(*)",
		Source:      "canary_results",
		GroupBy:     canaryGroupBy,
		Filter:      canaryFilter,
	})
	return r
}
