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

package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/shankar0123/netsite/pkg/anomaly"
	"github.com/shankar0123/netsite/pkg/api/middleware"
)

// What: read-only HTTP surface over the anomaly detector's cached
// verdicts. The evaluator goroutine in cmd/ns-controlplane writes
// the rows; these handlers read them.
//
// How:
//   - GET /v1/anomaly/state               → ListVerdicts(tenant) — every
//                                           cached verdict for the tenant.
//   - GET /v1/anomaly/tests/{id}          → GetVerdict(tenant, test, "latency_p95")
//                                           — convenience for the canary detail page;
//                                           assumes the default metric kind.
//   - GET /v1/anomaly/tests/{id}/{metric} → GetVerdict(tenant, test, metric) for
//                                           multi-metric setups.
//
// All three are anomaly:read (viewer+). Write operations land in
// v0.0.20 alongside the calendar-suppression CRUD; for v0.0.19 the
// verdicts are produced exclusively by the evaluator.
//
// Why no /v1/anomaly/evaluate POST: forcing an evaluation from a
// REST handler is a trap (it lets one operator spam ClickHouse with
// queries by mashing a button). Operators who want fresh state pass
// `--interval 30s` to the controlplane's NETSITE_ANOMALY_INTERVAL
// env var; v0.0.20 may add a "evaluate one" admin endpoint after the
// notifier work makes it useful.

// anomalyResponse mirrors anomaly.VerdictRow as a JSON-friendly
// shape. Field order matches the table column order so a developer
// debugging at the SQL layer reads the same shape.
type anomalyResponse struct {
	TenantID    string    `json:"tenant_id"`
	TestID      string    `json:"test_id"`
	Metric      string    `json:"metric"`
	Method      string    `json:"method"`
	Severity    string    `json:"severity"`
	Suppressed  bool      `json:"suppressed"`
	LastValue   float64   `json:"last_value"`
	Forecast    float64   `json:"forecast"`
	Residual    float64   `json:"residual"`
	MAD         float64   `json:"mad"`
	MADUnits    float64   `json:"mad_units"`
	Reason      string    `json:"reason"`
	LastPointAt time.Time `json:"last_point_at"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

func toAnomalyResponse(v anomaly.VerdictRow) anomalyResponse {
	return anomalyResponse{
		TenantID:    v.TenantID,
		TestID:      v.TestID,
		Metric:      v.Metric,
		Method:      string(v.Method),
		Severity:    string(v.Severity),
		Suppressed:  v.Suppressed,
		LastValue:   v.LastValue,
		Forecast:    v.Forecast,
		Residual:    v.Residual,
		MAD:         v.MAD,
		MADUnits:    v.MADUnits,
		Reason:      v.Reason,
		LastPointAt: v.LastPointAt,
		EvaluatedAt: v.EvaluatedAt,
	}
}

// listAnomalyStateHandler returns GET /v1/anomaly/state.
func listAnomalyStateHandler(store *anomaly.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows, err := store.ListVerdicts(r.Context(), u.TenantID)
		if err != nil {
			http.Error(w, "list anomaly state: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]anomalyResponse, 0, len(rows))
		for _, r := range rows {
			out = append(out, toAnomalyResponse(r))
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// getAnomalyForTestHandler returns GET /v1/anomaly/tests/{id} —
// convenience that assumes the default metric kind (latency_p95).
// 404 when no verdict is cached for that (tenant, test, metric).
func getAnomalyForTestHandler(store *anomaly.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testID := r.PathValue("id")
		if testID == "" {
			http.Error(w, "missing test id", http.StatusBadRequest)
			return
		}
		row, err := store.GetVerdict(r.Context(), u.TenantID, testID, string(anomaly.MetricLatencyP95))
		if err != nil {
			if errors.Is(err, anomaly.ErrVerdictNotFound) {
				http.Error(w, "anomaly verdict not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get anomaly verdict: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toAnomalyResponse(row))
	})
}

// getAnomalyForTestMetricHandler returns GET /v1/anomaly/tests/{id}/{metric}.
// Same shape as the convenience handler but the metric is explicit.
func getAnomalyForTestMetricHandler(store *anomaly.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testID := r.PathValue("id")
		metric := r.PathValue("metric")
		if testID == "" || metric == "" {
			http.Error(w, "missing test id or metric", http.StatusBadRequest)
			return
		}
		row, err := store.GetVerdict(r.Context(), u.TenantID, testID, metric)
		if err != nil {
			if errors.Is(err, anomaly.ErrVerdictNotFound) {
				http.Error(w, "anomaly verdict not found", http.StatusNotFound)
				return
			}
			http.Error(w, "get anomaly verdict: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, toAnomalyResponse(row))
	})
}
