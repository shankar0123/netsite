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

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// TestRun_HappyPath drives Run against an httptest server returning
// 204; verifies the timing fields are non-zero and the status is
// captured.
func TestRun_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r := New("pop-1")
	got := r.Run(context.Background(), canary.Test{
		ID: "tst-1", TenantID: "tnt-x", Target: srv.URL, Timeout: 5 * time.Second,
	})

	if got.StatusCode != http.StatusNoContent {
		t.Errorf("StatusCode = %d; want 204", got.StatusCode)
	}
	if got.ErrorKind != canary.ErrorKindNone {
		t.Errorf("ErrorKind = %q; want empty", got.ErrorKind)
	}
	if got.LatencyMs <= 0 {
		t.Errorf("LatencyMs = %v; want > 0", got.LatencyMs)
	}
	if got.TTFBMs <= 0 {
		t.Errorf("TTFBMs = %v; want > 0", got.TTFBMs)
	}
}

// TestRun_NotInExpectedRange asserts ExpectedStatus enforcement.
func TestRun_NotInExpectedRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := New("pop-1")
	got := r.Run(context.Background(), canary.Test{
		ID: "tst-1", Target: srv.URL, Timeout: 5 * time.Second,
		Config: Config{ExpectedStatus: "200-299"},
	})

	if got.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d; want 500", got.StatusCode)
	}
	if got.ErrorKind != canary.ErrorKindHTTPStatus {
		t.Errorf("ErrorKind = %q; want %q", got.ErrorKind, canary.ErrorKindHTTPStatus)
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

// TestStatusInRange covers the parsing branches of statusInRange.
func TestStatusInRange(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		expected string
		want     bool
	}{
		{"default 2xx ok", 200, "", true},
		{"default 2xx hi", 299, "", true},
		{"default 2xx fail", 301, "", false},
		{"exact match", 418, "418", true},
		{"exact mismatch", 200, "418", false},
		{"range hit", 250, "200-299", true},
		{"range miss low", 199, "200-299", false},
		{"range miss high", 300, "200-299", false},
		{"malformed empty range hi", 200, "200-", false},
		{"malformed nondigit", 200, "abc", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := statusInRange(tc.status, tc.expected); got != tc.want {
				t.Errorf("statusInRange(%d, %q) = %v; want %v", tc.status, tc.expected, got, tc.want)
			}
		})
	}
}
