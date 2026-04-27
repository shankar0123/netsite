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

// Package api is the HTTP surface of the NetSite control plane.
//
// What: a stdlib `net/http` server with the canonical NetSite middleware
// stack (logging → recovery → OTel) and an explicit route table. No
// third-party HTTP framework — architecture invariant in CLAUDE.md.
//
// How: New() builds a *Server from a Config. Run(ctx) blocks until ctx
// is canceled, then performs a 30-second graceful shutdown. Routes are
// registered against an http.ServeMux at construction time; adding a
// new route is a single mux.Handle call.
//
// Why a struct (not a free `func ListenAndServe`): tests need to
// construct an isolated server, point it at a httptest.Server-equivalent,
// and exercise it. A struct gives the tests a handle.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/shankar0123/netsite/pkg/api/middleware"
	promstore "github.com/shankar0123/netsite/pkg/store/prometheus"
)

// Config holds the dependencies New() needs. All fields are required.
//
// Why pgxpool.Pool here rather than a Storer interface: the dependencies
// are passed once at construction. Service-level handlers receive scoped
// interfaces (e.g. authService) that hide the pool. Threading an
// interface at the server level is over-abstraction at this stage.
type Config struct {
	// Addr is the listen address (e.g. ":8080" or "127.0.0.1:8080").
	Addr string
	// Pool is the Postgres connection pool. Used by health and (in
	// later tasks) auth, RBAC, and tenant-scoped handlers.
	Pool *pgxpool.Pool
	// Logger is the structured logger used by middleware and handlers.
	Logger *slog.Logger
	// PromReg is the Prometheus registry whose metrics are exposed at
	// /metrics. Service code registers per-feature metrics on it.
	PromReg *prometheus.Registry
	// Auth is the authentication service backing /v1/auth/*. Required.
	Auth authService
}

// Server is an HTTP server bound to the NetSite control-plane API
// surface. Construct with New, run with Run.
type Server struct {
	cfg     Config
	httpSrv *http.Server
}

// shutdownTimeout bounds how long Run waits for in-flight requests
// to drain after the parent context cancels. Long enough that a
// long-poll request can finish; short enough that operators get
// a deterministic SIGTERM-to-exit budget.
const shutdownTimeout = 30 * time.Second

// New constructs a Server. It validates required fields, builds the
// route table, and composes middleware. It does not bind a network
// socket; that happens in Run.
func New(cfg Config) (*Server, error) {
	if cfg.Addr == "" {
		return nil, errors.New("api: empty Addr")
	}
	if cfg.Pool == nil {
		return nil, errors.New("api: nil Pool")
	}
	if cfg.Logger == nil {
		return nil, errors.New("api: nil Logger")
	}
	if cfg.PromReg == nil {
		return nil, errors.New("api: nil PromReg")
	}
	if cfg.Auth == nil {
		return nil, errors.New("api: nil Auth")
	}

	mux := http.NewServeMux()

	// Health endpoint. Public — no auth, no RBAC.
	mux.Handle("GET /v1/health", healthHandler(cfg))

	// Auth endpoints.
	//   POST /v1/auth/login   — anonymous; returns user + sets cookie.
	//   POST /v1/auth/logout  — idempotent; clears cookie.
	//   GET  /v1/auth/whoami  — requires authenticated session.
	mux.Handle("POST /v1/auth/login", loginHandler(cfg.Auth))
	mux.Handle("POST /v1/auth/logout", logoutHandler(cfg.Auth))
	mux.Handle("GET /v1/auth/whoami", whoamiHandler())

	// Tests catalog. Per CLAUDE.md naming, /v1/<resource> plural.
	// RBAC: tests:read for viewer+, tests:write for operator+.
	mux.Handle("GET /v1/tests", middleware.Authorize("tests:read", listTestsHandler(cfg.Pool)))
	mux.Handle("POST /v1/tests", middleware.Authorize("tests:write", createTestHandler(cfg.Pool)))
	mux.Handle("GET /v1/tests/{id}", middleware.Authorize("tests:read", getTestHandler(cfg.Pool)))
	mux.Handle("DELETE /v1/tests/{id}", middleware.Authorize("tests:write", deleteTestHandler(cfg.Pool)))

	// POP roster. Same RBAC pattern.
	mux.Handle("GET /v1/pops", middleware.Authorize("pops:read", listPopsHandler(cfg.Pool)))
	mux.Handle("POST /v1/pops", middleware.Authorize("pops:write", createPopHandler(cfg.Pool)))
	mux.Handle("GET /v1/pops/{id}", middleware.Authorize("pops:read", getPopHandler(cfg.Pool)))
	mux.Handle("DELETE /v1/pops/{id}", middleware.Authorize("pops:write", deletePopHandler(cfg.Pool)))

	// Prometheus metrics. Public on the dev stack; in production this
	// is reachable only inside the cluster network.
	if err := promstore.ExposeOn(mux, cfg.PromReg); err != nil {
		return nil, fmt.Errorf("api: expose /metrics: %w", err)
	}

	// Middleware composition (outermost first):
	//   logging → recovery → authenticate → mux
	// Authenticate populates the user in context for routes that opt
	// in via middleware.Authorize. Login/logout/health are public and
	// see Authenticate as a no-op.
	handler := middleware.Authenticate(cfg.Auth, mux)
	handler = middleware.Recover(cfg.Logger, handler)
	handler = middleware.Logging(cfg.Logger, handler)

	return &Server{
		cfg: cfg,
		httpSrv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}, nil
}

// Addr returns the configured listen address. Useful in tests that
// listen on :0 and need to discover the port post-bind via the
// underlying http.Server (see Server.Listen for that flow).
func (s *Server) Addr() string {
	return s.httpSrv.Addr
}

// Handler returns the composed http.Handler the server uses. Tests
// can drive this directly through httptest.NewServer without binding
// a socket.
func (s *Server) Handler() http.Handler {
	return s.httpSrv.Handler
}

// Run starts the server and blocks until ctx is canceled or the
// server returns an error. On ctx cancellation it performs a
// graceful shutdown bounded by shutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		// http.ErrServerClosed is the expected return from a normal
		// Shutdown; do not surface it as an error to the caller.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.httpSrv.Shutdown(sctx); err != nil {
			return fmt.Errorf("api: graceful shutdown: %w", err)
		}
		return nil
	}
}
