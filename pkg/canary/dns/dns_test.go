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

package dns

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// TestRun_BadConfig asserts an empty target produces a bad-config
// result, not a panic.
func TestRun_BadConfig(t *testing.T) {
	r := New("pop-test")
	got := r.Run(context.Background(), canary.Test{ID: "tst-1", Target: ""})
	if got.ErrorKind != canary.ErrorKindBadConfig {
		t.Errorf("ErrorKind = %q; want %q", got.ErrorKind, canary.ErrorKindBadConfig)
	}
}

// TestRun_PopulatesCommonFields asserts that a Run populates the
// metadata fields regardless of network outcome.
func TestRun_PopulatesCommonFields(t *testing.T) {
	r := New("pop-abc")
	before := time.Now()
	got := r.Run(context.Background(), canary.Test{
		ID: "tst-1", TenantID: "tnt-x", Target: "localhost", Timeout: time.Second,
	})
	after := time.Now()

	if got.PopID != "pop-abc" {
		t.Errorf("PopID = %q; want %q", got.PopID, "pop-abc")
	}
	if got.TestID != "tst-1" {
		t.Errorf("TestID = %q; want %q", got.TestID, "tst-1")
	}
	if got.TenantID != "tnt-x" {
		t.Errorf("TenantID = %q; want %q", got.TenantID, "tnt-x")
	}
	if got.ObservedAt.Before(before) || got.ObservedAt.After(after.Add(time.Second)) {
		t.Errorf("ObservedAt = %v outside [%v..%v]", got.ObservedAt, before, after)
	}
}

// TestClassifyDNSError walks the error-classification matrix.
func TestClassifyDNSError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		ctxErr error
		want   string
	}{
		{"deadline", nil, context.DeadlineExceeded, canary.ErrorKindTimeout},
		{"dns timeout", &net.DNSError{IsTimeout: true}, nil, canary.ErrorKindTimeout},
		{"generic", errors.New("nope"), nil, canary.ErrorKindDNSResolve},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDNSError(tc.err, tc.ctxErr); got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}
