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
	"net/http"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/shankar0123/netsite/pkg/api/middleware"
)

// What: GET /v1/tests/{id}/results — return the most recent N
// canary results for a test, scoped to the caller's tenant.
//
// How: a single ClickHouse query against canary_results filtered by
// (tenant_id, test_id) ordered by observed_at DESC. Bound LIMIT to
// 1000 to avoid accidental large reads.
//
// Why now: operators creating their first SLO want to see what
// canary_results looks like. Without this endpoint they would have
// to shell into the ClickHouse container or run a separate query
// tool. A small read endpoint is the cheapest way to make the
// loop visible end-to-end.

// resultRow is the JSON shape returned per row.
type resultRow struct {
	TestID     string    `json:"test_id"`
	PopID      string    `json:"pop_id"`
	ObservedAt time.Time `json:"observed_at"`
	LatencyMs  float32   `json:"latency_ms"`
	DNSMs      float32   `json:"dns_ms"`
	ConnectMs  float32   `json:"connect_ms"`
	TLSMs      float32   `json:"tls_ms"`
	TTFBMs     float32   `json:"ttfb_ms"`
	StatusCode uint16    `json:"status_code"`
	ErrorKind  string    `json:"error_kind"`
}

// listTestResultsHandler returns GET /v1/tests/{id}/results.
//
// Query parameters:
//
//	limit   1..1000 (default 100)
func listTestResultsHandler(ch driver.Conn) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testID := r.PathValue("id")
		if testID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			if n > 1000 {
				n = 1000
			}
			limit = n
		}
		rows, err := ch.Query(r.Context(), `
            SELECT test_id, pop_id, observed_at,
                   latency_ms, dns_ms, connect_ms, tls_ms, ttfb_ms,
                   status_code, error_kind
            FROM canary_results
            WHERE tenant_id = ? AND test_id = ?
            ORDER BY observed_at DESC
            LIMIT ?`,
			u.TenantID, testID, limit)
		if err != nil {
			http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rows.Close() }()
		out := []resultRow{}
		for rows.Next() {
			var rr resultRow
			if err := rows.Scan(&rr.TestID, &rr.PopID, &rr.ObservedAt,
				&rr.LatencyMs, &rr.DNSMs, &rr.ConnectMs, &rr.TLSMs, &rr.TTFBMs,
				&rr.StatusCode, &rr.ErrorKind); err != nil {
				http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
				return
			}
			out = append(out, rr)
		}
		writeJSON(w, http.StatusOK, out)
	})
}
