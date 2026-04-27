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

package tls

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// TestRun_HappyPath_AgainstHTTPSTestServer asserts a successful
// handshake against an httptest TLS server populates the timing
// fields.
func TestRun_HappyPath_AgainstHTTPSTestServer(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()

	// httptest.NewTLSServer issues a self-signed cert; verification
	// must be disabled or the handshake will fail.
	r := &Runner{PopID: "pop-1", InsecureSkipVerify: true}
	host := strings.TrimPrefix(srv.URL, "https://")
	got := r.Run(context.Background(), canary.Test{
		ID: "tst-1", Target: host, Timeout: 5 * time.Second,
	})

	if got.ErrorKind != canary.ErrorKindNone {
		t.Errorf("ErrorKind = %q; want empty", got.ErrorKind)
	}
	if got.LatencyMs <= 0 {
		t.Errorf("LatencyMs = %v; want > 0", got.LatencyMs)
	}
	if got.TLSMs <= 0 {
		t.Errorf("TLSMs = %v; want > 0", got.TLSMs)
	}
}

// TestRun_BadTarget asserts an empty Target produces a bad-config
// result, not a panic.
func TestRun_BadTarget(t *testing.T) {
	r := New("pop-1")
	got := r.Run(context.Background(), canary.Test{Target: ""})
	if got.ErrorKind != canary.ErrorKindBadConfig {
		t.Errorf("ErrorKind = %q; want %q", got.ErrorKind, canary.ErrorKindBadConfig)
	}
}

// TestSplitHostPort exercises both branches of splitHostPort.
func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort string
	}{
		{"example.com:443", "example.com", "443"},
		{"example.com:8443", "example.com", "8443"},
		{"example.com", "example.com", "443"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			h, p := splitHostPort(tc.in)
			if h != tc.wantHost || p != tc.wantPort {
				t.Errorf("splitHostPort(%q) = (%q, %q); want (%q, %q)", tc.in, h, p, tc.wantHost, tc.wantPort)
			}
		})
	}
}
