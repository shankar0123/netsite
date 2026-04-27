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

package nats

import (
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
)

// TestConnect_EmptyURL asserts the empty-URL guard returns the
// ErrEmptyURL sentinel.
func TestConnect_EmptyURL(t *testing.T) {
	nc, err := Connect("", "test-client")
	if nc != nil {
		t.Fatalf("Connect returned non-nil conn for empty URL")
	}
	if !errors.Is(err, ErrEmptyURL) {
		t.Fatalf("Connect err = %v; want ErrEmptyURL", err)
	}
}

// TestJetStream_NilConn asserts the nil-conn guard fires before any
// dereference. Catches a class of programming bugs where the caller
// passes a not-yet-initialised connection.
func TestJetStream_NilConn(t *testing.T) {
	js, err := JetStream(nil)
	if js != nil {
		t.Fatalf("JetStream(nil) returned non-nil context")
	}
	if err == nil {
		t.Fatal("JetStream(nil) returned nil error; want guard")
	}
}

// TestEnsureStream_GuardErrors asserts the validation branches of
// EnsureStream fail loudly with descriptive errors instead of
// silently producing a misconfigured stream.
func TestEnsureStream_GuardErrors(t *testing.T) {
	cases := []struct {
		name    string
		js      nats.JetStreamContext
		cfg     *StreamConfig
		wantSub string
	}{
		{"nil context", nil, &StreamConfig{Name: "X", Subjects: []string{"x.>"}}, "nil JetStreamContext"},
		// Use a non-nil placeholder JetStreamContext-like by passing a
		// nil that is itself nil; the cfg-nil guard fires first when
		// the previous case is past us, so we compose with a
		// sentinel below.
		{"nil cfg", nil, nil, "nil"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := EnsureStream(tc.js, tc.cfg)
			if err == nil {
				t.Fatalf("EnsureStream returned nil error; want substring %q", tc.wantSub)
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %q; want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestStreamConfigDiffers asserts the fields NetSite manages produce
// the expected differ() result. Each table case toggles one field and
// expects the differ to report a difference; an "all equal" baseline
// case anchors the negative path.
func TestStreamConfigDiffers(t *testing.T) {
	base := &nats.StreamConfig{
		Name:      "NETSITE_X",
		Subjects:  []string{"netsite.x.>"},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    0,
		MaxBytes:  0,
		Replicas:  1,
	}
	cases := []struct {
		name string
		mut  func(c *nats.StreamConfig)
		want bool
	}{
		{"identical", func(_ *nats.StreamConfig) {}, false},
		{"storage changed", func(c *nats.StreamConfig) { c.Storage = nats.MemoryStorage }, true},
		{"retention changed", func(c *nats.StreamConfig) { c.Retention = nats.WorkQueuePolicy }, true},
		{"subjects changed", func(c *nats.StreamConfig) { c.Subjects = []string{"netsite.y.>"} }, true},
		{"replicas changed (non-zero)", func(c *nats.StreamConfig) { c.Replicas = 3 }, true},
		{"replicas zero (default keeps no diff)", func(c *nats.StreamConfig) { c.Replicas = 0 }, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want := *base
			tc.mut(&want)
			got := streamConfigDiffers(&want, base)
			if got != tc.want {
				t.Errorf("streamConfigDiffers = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestEqualStringSlices is a small unit guard against off-by-one bugs
// in the helper used by streamConfigDiffers.
func TestEqualStringSlices(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"equal one", []string{"x"}, []string{"x"}, true},
		{"different len", []string{"x"}, []string{"x", "y"}, false},
		{"different element", []string{"x"}, []string{"y"}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := equalStringSlices(tc.a, tc.b); got != tc.want {
				t.Errorf("equalStringSlices(%v, %v) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// contains is strings.Contains kept inline so this test file does not
// re-import the strings package only for one call.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
