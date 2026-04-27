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
	"bytes"
	"strings"
	"testing"
)

// TestNs_RootCmd_HasVersionSubcommand asserts the constructor wires the
// `version` subcommand. If a future refactor breaks the wiring, callers
// scripted on `ns version` would silently start failing — this test
// catches it at build time.
func TestNs_RootCmd_HasVersionSubcommand(t *testing.T) {
	root := newRootCmd()

	for _, want := range []string{"version"} {
		want := want
		t.Run(want, func(t *testing.T) {
			cmd, _, err := root.Find([]string{want})
			if err != nil {
				t.Fatalf("root.Find(%q) returned error: %v", want, err)
			}
			if cmd == nil || cmd.Name() != want {
				t.Fatalf("subcommand %q not registered on root", want)
			}
		})
	}
}

// TestNs_Run_VersionSubcommand asserts that run() with ["version"]
// writes a banner containing the expected tokens to stdout and returns
// exit code 0. Going through run() (not newRootCmd().Execute()) is what
// keeps main()'s sibling logic covered without invoking os.Exit.
func TestNs_Run_VersionSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run([\"version\"]) = %d; want 0. stderr=%q", code, stderr.String())
	}

	got := stdout.String()
	cases := []struct {
		name string
		want string
	}{
		{"netsite prefix", "netsite "},
		{"commit token", "commit "},
		{"built token", "built "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("ns version stdout = %q; expected to contain %q", got, tc.want)
			}
		})
	}
}

// TestNs_Run_UnknownSubcommand asserts that an unknown subcommand causes
// run() to return a non-zero exit code. The CI relies on this behavior
// for `make build` failures to surface in scripted callers.
func TestNs_Run_UnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"definitely-not-a-real-command"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run with unknown subcommand returned 0; want non-zero. stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
