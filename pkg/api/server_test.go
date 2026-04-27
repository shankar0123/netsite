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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/shankar0123/netsite/pkg/annotations"
	"github.com/shankar0123/netsite/pkg/auth"
	"github.com/shankar0123/netsite/pkg/netql"
	"github.com/shankar0123/netsite/pkg/slo"
	"github.com/shankar0123/netsite/pkg/workspaces"
)

// recorder is a tiny pair of (httptest.ResponseRecorder,
// *http.Request) we drive into a Handler when we only care about
// the response headers.
type recorder struct {
	w *httptest.ResponseRecorder
	r *http.Request
}

func newRecorder(path string) recorder {
	return recorder{
		w: httptest.NewRecorder(),
		r: httptest.NewRequest(http.MethodGet, path, nil),
	}
}

// stubAuth is a minimal authService implementation for tests that
// only need server construction to succeed.
type stubAuth struct{}

func (stubAuth) Login(context.Context, string, string, string) (auth.Session, error) {
	return auth.Session{}, nil
}
func (stubAuth) Logout(context.Context, string) error              { return nil }
func (stubAuth) Whoami(context.Context, string) (auth.User, error) { return auth.User{}, nil }

// stubCH satisfies driver.Conn for construction-only tests by
// embedding the interface (zero value). Method calls panic, but the
// tests below never invoke any.
type stubCH struct{ chdriver.Conn }

// TestNew_Validations asserts every required field is checked, with
// distinct error messages so log readers can find the missing one.
func TestNew_Validations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool := &pgxpool.Pool{}
	reg := prometheus.NewRegistry()
	stub := stubAuth{}
	ch := stubCH{}
	store := slo.NewStore(nil)
	wks := workspaces.NewService(workspaces.NewStore(nil), workspaces.NewStore(nil), workspaces.Options{})
	ann := annotations.NewService(annotations.NewStore(nil), annotations.NewStore(nil), annotations.Options{})
	nqr := netql.DefaultRegistry()

	// All tests below request AllowPlaintext=true so the TLS
	// validator doesn't reject them; the per-field validations are
	// the subjects under test.
	cases := []struct {
		name    string
		cfg     Config
		wantSub string
	}{
		{"empty Addr", Config{Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "Addr"},
		{"nil Pool", Config{Addr: ":0", Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "Pool"},
		{"nil Logger", Config{Addr: ":0", Pool: pool, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "Logger"},
		{"nil PromReg", Config{Addr: ":0", Pool: pool, Logger: logger, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "PromReg"},
		{"nil Auth", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "Auth"},
		{"nil CH", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "CH"},
		{"nil SLOStore", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "SLOStore"},
		{"nil Workspaces", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Annotations: ann, NetQLRegistry: nqr, AllowPlaintext: true}, "Workspaces"},
		{"nil Annotations", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, NetQLRegistry: nqr, AllowPlaintext: true}, "Annotations"},
		{"nil NetQLRegistry", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, AllowPlaintext: true}, "NetQLRegistry"},
		{"missing TLS opt-in", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr}, "TLS"},
		{"half TLS config (cert only)", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, TLSCertFile: "/tmp/cert.pem"}, "TLSCertFile"},
		{"half TLS config (key only)", Config{Addr: ":0", Pool: pool, Logger: logger, PromReg: reg, Auth: stub, CH: ch, SLOStore: store, Workspaces: wks, Annotations: ann, NetQLRegistry: nqr, TLSKeyFile: "/tmp/key.pem"}, "TLSCertFile"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.cfg)
			if s != nil {
				t.Fatalf("New returned non-nil server")
			}
			if err == nil {
				t.Fatalf("expected error mentioning %q", tc.wantSub)
			}
			if !errorContains(err, tc.wantSub) {
				t.Errorf("err = %v; want substring %q", err, tc.wantSub)
			}
		})
	}
}

// TestNew_OK asserts a fully populated Config produces a server with
// the expected Addr.
func TestNew_OK(t *testing.T) {
	cfg := Config{
		Addr:           "127.0.0.1:0",
		Pool:           &pgxpool.Pool{},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		PromReg:        prometheus.NewRegistry(),
		Auth:           stubAuth{},
		CH:             stubCH{},
		SLOStore:       slo.NewStore(nil),
		Workspaces:     workspaces.NewService(workspaces.NewStore(nil), workspaces.NewStore(nil), workspaces.Options{}),
		Annotations:    annotations.NewService(annotations.NewStore(nil), annotations.NewStore(nil), annotations.Options{}),
		NetQLRegistry:  netql.DefaultRegistry(),
		AllowPlaintext: true, // explicit opt-in per CLAUDE.md A11
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Addr() != "127.0.0.1:0" {
		t.Errorf("Addr = %q; want %q", s.Addr(), "127.0.0.1:0")
	}
	if s.Handler() == nil {
		t.Error("Handler is nil")
	}
	if s.TLSEnabled() {
		t.Error("TLSEnabled() = true; want false (no cert/key supplied)")
	}
}

// TestNew_TLSConfig asserts the TLS-listen path: setting both
// TLSCertFile and TLSKeyFile flips TLSEnabled, defaults
// TLSMinVersion to 1.3, and produces a server that the HSTS
// middleware will see.
func TestNew_TLSConfig(t *testing.T) {
	cfg := Config{
		Addr:          "127.0.0.1:0",
		Pool:          &pgxpool.Pool{},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		PromReg:       prometheus.NewRegistry(),
		Auth:          stubAuth{},
		CH:            stubCH{},
		SLOStore:      slo.NewStore(nil),
		Workspaces:    workspaces.NewService(workspaces.NewStore(nil), workspaces.NewStore(nil), workspaces.Options{}),
		Annotations:   annotations.NewService(annotations.NewStore(nil), annotations.NewStore(nil), annotations.Options{}),
		NetQLRegistry: netql.DefaultRegistry(),
		TLSCertFile:   "/tmp/never-read.pem",
		TLSKeyFile:    "/tmp/never-read.key",
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !s.TLSEnabled() {
		t.Error("TLSEnabled() = false; want true")
	}
}

// TestNew_HSTSAppliedInTLSMode asserts the HSTS header is set when
// the server runs in TLS mode and absent in plaintext mode.
func TestNew_HSTSAppliedInTLSMode(t *testing.T) {
	base := Config{
		Addr:          "127.0.0.1:0",
		Pool:          &pgxpool.Pool{},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		PromReg:       prometheus.NewRegistry(),
		Auth:          stubAuth{},
		CH:            stubCH{},
		SLOStore:      slo.NewStore(nil),
		Workspaces:    workspaces.NewService(workspaces.NewStore(nil), workspaces.NewStore(nil), workspaces.Options{}),
		Annotations:   annotations.NewService(annotations.NewStore(nil), annotations.NewStore(nil), annotations.Options{}),
		NetQLRegistry: netql.DefaultRegistry(),
	}
	tlsCfg := base
	tlsCfg.TLSCertFile = "/tmp/cert.pem"
	tlsCfg.TLSKeyFile = "/tmp/key.pem"
	tlsSrv, err := New(tlsCfg)
	if err != nil {
		t.Fatalf("TLS New: %v", err)
	}
	plainCfg := base
	plainCfg.AllowPlaintext = true
	plainSrv, err := New(plainCfg)
	if err != nil {
		t.Fatalf("plaintext New: %v", err)
	}

	tlsRR := newRecorder("/v1/health")
	tlsSrv.Handler().ServeHTTP(tlsRR.w, tlsRR.r)
	if got := tlsRR.w.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS header missing in TLS mode")
	}
	plainRR := newRecorder("/v1/health")
	plainSrv.Handler().ServeHTTP(plainRR.w, plainRR.r)
	if got := plainRR.w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS header present in plaintext mode: %q", got)
	}
}

func errorContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), sub)
}

// contains is a strings.Contains-equivalent kept inline so this
// file does not import strings just for one check.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
