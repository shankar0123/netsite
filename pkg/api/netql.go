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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/shankar0123/netsite/pkg/api/middleware"
	"github.com/shankar0123/netsite/pkg/netql"
)

// What: HTTP handlers for /v1/netql/translate and /v1/netql/execute.
// translate is the "show me the SQL" reveal — Parse + TypeCheck +
// Translate, return the SQL/PromQL string + the bound args + which
// backend the metric lives in. execute is the operator-facing
// query path — translate, then run against ClickHouse, return the
// columnar rows.
//
// How: both endpoints take {"query":"..."} as the request body.
// The translate handler is pure (no DB), so it doesn't need the
// ClickHouse connection. The execute handler reuses the same
// translate path then dispatches to ClickHouse via the
// chdriver.Conn already in api.Config.
//
// Why two endpoints rather than one with a "?execute=true" flag:
// the React shell's "show me the SQL" UI surfaces both at once
// (compile every keystroke, execute on click), and splitting them
// keeps the unit tests focused — translate is a pure string-in /
// string-out check, execute needs a ClickHouse fixture. RBAC also
// differs — netql:read for translate (it's effectively a help
// text), netql:execute for execute (it actually consumes
// ClickHouse cycles).

// netqlTranslateRequest is the body of POST /v1/netql/translate.
type netqlTranslateRequest struct {
	Query string `json:"query"`
}

// netqlTranslateResponse mirrors the fields the React shell needs
// to render the "show me the SQL" panel: the surface-form SQL +
// the bound args (for parameter inspection) + which backend the
// metric lives in (so the UI can choose ClickHouse table
// rendering vs. PromQL chart rendering).
type netqlTranslateResponse struct {
	Backend string `json:"backend"`
	Metric  string `json:"metric"`
	SQL     string `json:"sql,omitempty"`    // populated when backend=clickhouse
	PromQL  string `json:"promql,omitempty"` // populated when backend=prometheus
	Args    []any  `json:"args,omitempty"`   // populated when backend=clickhouse
}

// netqlTranslateHandler returns POST /v1/netql/translate. RBAC:
// netql:read.
func netqlTranslateHandler(reg *netql.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req netqlTranslateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		out, err := compileNetQL(req.Query, reg, u.TenantID)
		if err != nil {
			netqlError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// netqlExecuteRequest is the body of POST /v1/netql/execute. The
// schema is a superset of translate's so a UI can submit one
// payload and switch endpoints based on a button click.
type netqlExecuteRequest struct {
	Query string `json:"query"`
}

// netqlExecuteResponse adds the columnar result data to the
// translation. Columns is the column-name list (same order as
// each Row); Rows is `[][]any` so the JSON shape is symmetric
// with the typical SQL-explorer pattern.
type netqlExecuteResponse struct {
	netqlTranslateResponse
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// netqlExecuteHandler returns POST /v1/netql/execute. RBAC:
// netql:execute.
func netqlExecuteHandler(reg *netql.Registry, ch chdriver.Conn) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := middleware.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req netqlExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		tr, err := compileNetQL(req.Query, reg, u.TenantID)
		if err != nil {
			netqlError(w, err)
			return
		}
		// Phase 0 ships ClickHouse execution only; PromQL execution
		// arrives when the controlplane has a Prometheus query
		// client wired (Phase 1). A PromQL query today returns 501
		// with the translated PromQL string in the response so the
		// operator can copy-paste it into their own Grafana.
		if tr.Backend != string(netql.BackendClickHouse) {
			writeJSON(w, http.StatusNotImplemented, struct {
				netqlTranslateResponse
				Note string `json:"note"`
			}{
				netqlTranslateResponse: tr,
				Note:                   "PromQL execution arrives in Phase 1; the translated query is returned for copy-paste into your Grafana.",
			})
			return
		}
		columns, rows, err := executeClickHouse(r.Context(), ch, tr.SQL, tr.Args)
		if err != nil {
			http.Error(w, "execute: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, netqlExecuteResponse{
			netqlTranslateResponse: tr,
			Columns:                columns,
			Rows:                   rows,
		})
	})
}

// compileNetQL is the shared parse + check + translate path. The
// returned struct carries enough information for both the
// "translate-only" reveal panel and the "execute" call site.
func compileNetQL(query string, reg *netql.Registry, tenantID string) (netqlTranslateResponse, error) {
	q, err := netql.Parse(query)
	if err != nil {
		return netqlTranslateResponse{}, err
	}
	if err := netql.Check(q, reg); err != nil {
		return netqlTranslateResponse{}, err
	}
	spec, _ := reg.Get(q.Metric)

	out := netqlTranslateResponse{
		Backend: string(spec.Backend),
		Metric:  spec.Name,
	}
	switch spec.Backend {
	case netql.BackendClickHouse:
		t, err := netql.TranslateClickHouse(q, reg, tenantID)
		if err != nil {
			return netqlTranslateResponse{}, err
		}
		out.SQL = t.SQL
		out.Args = t.Args
	case netql.BackendPrometheus:
		s, err := netql.TranslatePrometheus(q, reg)
		if err != nil {
			return netqlTranslateResponse{}, err
		}
		out.PromQL = s
	default:
		return netqlTranslateResponse{}, fmt.Errorf("netql: unknown backend %q", spec.Backend)
	}
	return out, nil
}

// executeClickHouse runs sql against the bound ClickHouse driver
// and converts the rows into the column-list + [][]any shape the
// REST surface returns.
func executeClickHouse(ctx context.Context, ch chdriver.Conn, sql string, args []any) ([]string, [][]any, error) {
	rows, err := ch.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("clickhouse query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols := rows.Columns()
	out := [][]any{}
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, fmt.Errorf("clickhouse scan: %w", err)
		}
		out = append(out, dest)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("clickhouse rows: %w", err)
	}
	return cols, out, nil
}

// netqlError maps the typed netql errors to HTTP status codes.
// Lex / Parse / Type errors are 400; Translate errors are 422
// (semantically valid input the backend can't fulfil — e.g., a
// metric exists but isn't ClickHouse-backed).
func netqlError(w http.ResponseWriter, err error) {
	var (
		lerr *netql.LexError
		perr *netql.ParseError
		terr *netql.TypeError
		xerr *netql.TranslateError
	)
	switch {
	case errors.As(err, &lerr) || errors.As(err, &perr):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.As(err, &terr):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.As(err, &xerr):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		http.Error(w, "netql: "+err.Error(), http.StatusInternalServerError)
	}
}
