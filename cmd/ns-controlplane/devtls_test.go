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

package main

import (
	"errors"
	"os"
	"testing"
)

// TestIsLoopbackAddr walks the matrix documented in devtls.go.
// The loopback predicate is the gate that prevents AutoTLS from
// minting a self-signed cert for a non-loopback bind — getting it
// wrong would defeat the whole point of the carve-out.
func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		// Loopback — accept.
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"127.0.0.1", true},
		{"localhost", true},
		// Not loopback — reject.
		{":8080", false},
		{"", false},
		{"0.0.0.0:8080", false},
		{"0.0.0.0", false},
		{"192.168.1.5:8080", false},
		{"example.com:8080", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := isLoopbackAddr(tc.addr); got != tc.want {
				t.Errorf("isLoopbackAddr(%q) = %v; want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestAutoTLS_RejectsNonLoopback asserts the gate fires.
func TestAutoTLS_RejectsNonLoopback(t *testing.T) {
	_, err := AutoTLS("0.0.0.0:8443")
	if !errors.Is(err, ErrNonLoopbackBind) {
		t.Fatalf("err = %v; want ErrNonLoopbackBind", err)
	}
}

// TestAutoTLS_LoopbackHappyPath round-trips a cert generation. We
// pin to a loopback address, generate, and confirm the files were
// written + the fingerprint is non-empty.
func TestAutoTLS_LoopbackHappyPath(t *testing.T) {
	res, err := AutoTLS("127.0.0.1:0")
	if err != nil {
		t.Fatalf("AutoTLS: %v", err)
	}
	if res.CertFile == "" || res.KeyFile == "" {
		t.Errorf("empty path(s) in result %+v", res)
	}
	if len(res.SHA256Fingerprint) != 64 {
		t.Errorf("SHA256Fingerprint = %q (len %d); want 64-hex", res.SHA256Fingerprint, len(res.SHA256Fingerprint))
	}
	for _, p := range []string{res.CertFile, res.KeyFile} {
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("stat %q: %v", p, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%q is empty", p)
		}
	}
}
