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
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/shankar0123/netsite/pkg/annotations"
	"github.com/shankar0123/netsite/pkg/anomaly"
	"github.com/shankar0123/netsite/pkg/api/middleware"
	"github.com/shankar0123/netsite/pkg/netql"
	"github.com/shankar0123/netsite/pkg/slo"
	promstore "github.com/shankar0123/netsite/pkg/store/prometheus"
	"github.com/shankar0123/netsite/pkg/workspaces"
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
	// CH is the ClickHouse connection used by canary-result query
	// endpoints (/v1/tests/{id}/results). Required.
	CH driver.Conn
	// SLOStore backs the /v1/slos handlers. Required.
	SLOStore *slo.Store
	// Workspaces backs /v1/workspaces and /v1/share/{slug}. Required.
	Workspaces *workspaces.Service
	// Annotations backs /v1/annotations. Required.
	Annotations *annotations.Service
	// NetQLRegistry backs /v1/netql/{translate,execute}. Required.
	NetQLRegistry *netql.Registry
	// AnomalyStore backs /v1/anomaly/*. Required — the evaluator
	// goroutine is the writer; these handlers are read-only over the
	// cached verdicts.
	AnomalyStore *anomaly.Store

	// TLSCertFile / TLSKeyFile point at PEM-encoded certificate and
	// private key files for TLS-listen mode. When both are set the
	// server binds via ListenAndServeTLS, mounts the HSTS middleware,
	// and rejects plaintext connections.
	//
	// Why operator-supplied cert files rather than ACME / Let's
	// Encrypt automation: NetSite is a self-hosted product. Operators
	// already manage TLS certs for their other infrastructure (cert-
	// manager in k8s, Caddy in single-node, manual cron in air-gap).
	// Adding ACME would be one more dependency for a problem the
	// operator already solved.
	TLSCertFile string
	TLSKeyFile  string

	// AllowPlaintext lets the server start without TLS. The
	// constitution (CLAUDE.md A11) requires deliberate opt-in: an
	// operator unsetting both TLS{Cert,Key}File AND failing to set
	// AllowPlaintext=true gets a hard error at New(). The opt-in is
	// also surfaced as a Warn-level log line at boot so the
	// plaintext posture is never silent.
	AllowPlaintext bool

	// TLSMinVersion is the minimum protocol version when TLS is
	// active. Defaults to tls.VersionTLS13 when zero. TLS 1.2 is
	// supported behind an explicit operator opt-in for environments
	// with legacy clients (e.g., enterprise proxies that haven't
	// rolled TLS 1.3 yet). Anything below TLS 1.2 is hard-rejected
	// because all known production clients support 1.2 by 2026.
	TLSMinVersion uint16
}

// Server is an HTTP(S) server bound to the NetSite control-plane API
// surface. Construct with New, run with Run.
type Server struct {
	cfg        Config
	httpSrv    *http.Server
	tlsEnabled bool
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
	if cfg.CH == nil {
		return nil, errors.New("api: nil CH")
	}
	if cfg.SLOStore == nil {
		return nil, errors.New("api: nil SLOStore")
	}
	if cfg.Workspaces == nil {
		return nil, errors.New("api: nil Workspaces")
	}
	if cfg.Annotations == nil {
		return nil, errors.New("api: nil Annotations")
	}
	if cfg.NetQLRegistry == nil {
		return nil, errors.New("api: nil NetQLRegistry")
	}
	if cfg.AnomalyStore == nil {
		return nil, errors.New("api: nil AnomalyStore")
	}
	tlsEnabled, err := validateTLSConfig(&cfg)
	if err != nil {
		return nil, err
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

	// Canary results query. tests:read suffices because these are
	// reads of operational telemetry, not catalog mutations.
	mux.Handle("GET /v1/tests/{id}/results", middleware.Authorize("tests:read", listTestResultsHandler(cfg.CH)))

	// SLO catalog.
	mux.Handle("GET /v1/slos", middleware.Authorize("slos:read", listSLOsHandler(cfg.SLOStore)))
	mux.Handle("POST /v1/slos", middleware.Authorize("slos:write", createSLOHandler(cfg.SLOStore)))
	mux.Handle("GET /v1/slos/{id}", middleware.Authorize("slos:read", getSLOHandler(cfg.SLOStore)))
	mux.Handle("DELETE /v1/slos/{id}", middleware.Authorize("slos:write", deleteSLOHandler(cfg.SLOStore)))

	// Workspaces. Saved-view bundles. RBAC: workspaces:read for
	// viewer+, workspaces:write for operator+. /v1/share/{slug} is
	// public (the slug is the access control); see resolveShareHandler
	// for the strip-tenant-and-owner logic.
	mux.Handle("GET /v1/workspaces", middleware.Authorize("workspaces:read", listWorkspacesHandler(cfg.Workspaces)))
	mux.Handle("POST /v1/workspaces", middleware.Authorize("workspaces:write", createWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("GET /v1/workspaces/{id}", middleware.Authorize("workspaces:read", getWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("PATCH /v1/workspaces/{id}", middleware.Authorize("workspaces:write", updateWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("DELETE /v1/workspaces/{id}", middleware.Authorize("workspaces:write", deleteWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("POST /v1/workspaces/{id}/share", middleware.Authorize("workspaces:write", shareWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("DELETE /v1/workspaces/{id}/share", middleware.Authorize("workspaces:write", unshareWorkspaceHandler(cfg.Workspaces)))
	mux.Handle("GET /v1/share/{slug}", resolveShareHandler(cfg.Workspaces))

	// Annotations. Pinned operator notes scoped to (canary | pop |
	// test, scope_id, timestamp). RBAC: viewer+ for read,
	// operator+ for write. Immutable by design — no PATCH endpoint.
	mux.Handle("GET /v1/annotations", middleware.Authorize("annotations:read", listAnnotationsHandler(cfg.Annotations)))
	mux.Handle("POST /v1/annotations", middleware.Authorize("annotations:write", createAnnotationHandler(cfg.Annotations)))
	mux.Handle("GET /v1/annotations/{id}", middleware.Authorize("annotations:read", getAnnotationHandler(cfg.Annotations)))
	mux.Handle("DELETE /v1/annotations/{id}", middleware.Authorize("annotations:write", deleteAnnotationHandler(cfg.Annotations)))

	// netql DSL — the "show me the SQL" reveal + the executor.
	// translate is netql:read because it's effectively help text;
	// execute is netql:execute because it consumes ClickHouse cycles.
	mux.Handle("POST /v1/netql/translate", middleware.Authorize("netql:read", netqlTranslateHandler(cfg.NetQLRegistry)))
	mux.Handle("POST /v1/netql/execute", middleware.Authorize("netql:execute", netqlExecuteHandler(cfg.NetQLRegistry, cfg.CH)))

	// Anomaly detector verdicts. Read-only — the evaluator goroutine
	// in cmd/ns-controlplane is the writer. v0.0.20 adds calendar-
	// suppression CRUD + (optional) on-demand evaluate endpoint.
	mux.Handle("GET /v1/anomaly/state", middleware.Authorize("anomaly:read", listAnomalyStateHandler(cfg.AnomalyStore)))
	mux.Handle("GET /v1/anomaly/tests/{id}", middleware.Authorize("anomaly:read", getAnomalyForTestHandler(cfg.AnomalyStore)))
	mux.Handle("GET /v1/anomaly/tests/{id}/{metric}", middleware.Authorize("anomaly:read", getAnomalyForTestMetricHandler(cfg.AnomalyStore)))

	// Prometheus metrics. Public on the dev stack; in production this
	// is reachable only inside the cluster network.
	if err := promstore.ExposeOn(mux, cfg.PromReg); err != nil {
		return nil, fmt.Errorf("api: expose /metrics: %w", err)
	}

	// Middleware composition (outermost first):
	//   logging → recovery → [hsts when TLS] → authenticate → mux
	// Authenticate populates the user in context for routes that opt
	// in via middleware.Authorize. Login/logout/health are public and
	// see Authenticate as a no-op. HSTS only mounts in TLS-listen
	// mode — telling a browser "always use TLS" while we're talking
	// over plain HTTP would break the very server it just hit.
	handler := middleware.Authenticate(cfg.Auth, mux)
	if tlsEnabled {
		handler = middleware.HSTS(handler)
	} else {
		// Plaintext mode is opt-in (CLAUDE.md A11). Log it loudly so
		// the operator sees the warning in production logs and
		// distinguishes a misconfigured deployment from a deliberate
		// one.
		cfg.Logger.Warn("api: TLS disabled (NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true). Production deployments must terminate TLS upstream or set TLSCertFile/TLSKeyFile.")
	}
	handler = middleware.Recover(cfg.Logger, handler)
	handler = middleware.Logging(cfg.Logger, handler)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if tlsEnabled {
		srv.TLSConfig = &tls.Config{MinVersion: cfg.TLSMinVersion}
	}
	return &Server{cfg: cfg, httpSrv: srv, tlsEnabled: tlsEnabled}, nil
}

// validateTLSConfig enforces architecture invariant A11: every
// operator-facing network surface defaults to TLS 1.3+. Returns
// whether the server should listen in TLS mode, plus a wrapped
// error when the config is incoherent.
//
// The matrix:
//
//	cert AND key set, plaintext=false → TLS, valid.
//	cert AND key set, plaintext=true  → TLS (cert wins; warn? today: ok).
//	cert OR key set (not both)        → invalid.
//	neither set, plaintext=true       → plaintext (warn at boot).
//	neither set, plaintext=false      → invalid (refuse to start).
//
// Default min TLS version is 1.3. Operators can opt down to 1.2 by
// setting TLSMinVersion explicitly; anything lower is rejected.
func validateTLSConfig(cfg *Config) (bool, error) {
	hasCert := cfg.TLSCertFile != ""
	hasKey := cfg.TLSKeyFile != ""
	if hasCert != hasKey {
		return false, errors.New("api: TLSCertFile and TLSKeyFile must be set together")
	}
	tlsEnabled := hasCert && hasKey
	if !tlsEnabled && !cfg.AllowPlaintext {
		return false, errors.New("api: refusing to start without TLS; set TLSCertFile+TLSKeyFile or AllowPlaintext=true (NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true) to opt into plaintext")
	}
	if tlsEnabled {
		if cfg.TLSMinVersion == 0 {
			cfg.TLSMinVersion = tls.VersionTLS13
		}
		if cfg.TLSMinVersion < tls.VersionTLS12 {
			return false, fmt.Errorf("api: TLSMinVersion must be ≥ TLS 1.2 (got 0x%x)", cfg.TLSMinVersion)
		}
	}
	return tlsEnabled, nil
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

// SetHandler replaces the http.Server's outermost handler. The
// canonical use is composing the API surface with an embedded SPA
// in cmd/ns-controlplane: the caller takes Handler(), wraps it with
// a router that delegates /v1/* to the API and everything else to
// the SPA, and writes the result back via SetHandler.
//
// Why expose a setter rather than accept a wrapper at construction:
// the embed-vs-not decision belongs in the binary, not the api
// package — keeping api free of web/embed coupling lets us reuse it
// from ns-pop / ns-bgp for their own admin endpoints later without
// dragging in a frontend dependency.
func (s *Server) SetHandler(h http.Handler) {
	s.httpSrv.Handler = h
}

// TLSEnabled reports whether the server will bind via
// ListenAndServeTLS. Useful for tests + boot-time logging.
func (s *Server) TLSEnabled() bool { return s.tlsEnabled }

// Run starts the server and blocks until ctx is canceled or the
// server returns an error. On ctx cancellation it performs a
// graceful shutdown bounded by shutdownTimeout.
//
// TLS-listen mode (when both TLSCertFile and TLSKeyFile are set):
//   - Binds via ListenAndServeTLS using the configured min protocol
//     version (default: TLS 1.3).
//   - Mounts the HSTS middleware so browsers refuse plaintext
//     downgrade for the next year.
//
// Plaintext mode requires explicit AllowPlaintext=true (CLAUDE.md
// A11). The Warn-level boot log emitted in New() makes the
// posture visible in production logs.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if s.tlsEnabled {
			errCh <- s.httpSrv.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
			return
		}
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
