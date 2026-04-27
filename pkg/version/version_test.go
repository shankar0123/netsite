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

package version

import (
	"strings"
	"testing"
)

// TestVersion_PackageVars_NonEmpty asserts that the build-time-injected
// package variables have non-empty defaults. A blank default would
// silently produce broken `ns version` output and an empty
// `X-NetSite-Version` header on an unsigned local build.
func TestVersion_PackageVars_NonEmpty(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"Version", Version},
		{"Commit", Commit},
		{"BuildDate", BuildDate},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == "" {
				t.Fatalf("%s default is empty; expected a sentinel", tc.name)
			}
		})
	}
}

// TestVersion_String_ContainsAllFields asserts that String() includes
// each of the three injected values. The `ns version` UX and downstream
// log/metric labels rely on this being human-greppable.
func TestVersion_String_ContainsAllFields(t *testing.T) {
	got := String()
	cases := []struct {
		name string
		want string
	}{
		{"netsite prefix", "netsite "},
		{"version field", Version},
		{"commit field", Commit},
		{"build date field", BuildDate},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("String() = %q; expected to contain %q", got, tc.want)
			}
		})
	}
}

// TestVersion_String_LdflagsOverridable asserts that String() reflects
// any change to the package-level vars after init. This is the property
// the Makefile relies on when injecting -X overrides.
func TestVersion_String_LdflagsOverridable(t *testing.T) {
	origVersion, origCommit, origBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = origVersion, origCommit, origBuildDate
	})

	cases := []struct {
		name      string
		version   string
		commit    string
		buildDate string
		wantSub   string
	}{
		{
			name:      "release tag injected",
			version:   "v0.0.1",
			commit:    "abc1234",
			buildDate: "2026-04-26T00:00:00Z",
			wantSub:   "netsite v0.0.1 (commit abc1234, built 2026-04-26T00:00:00Z)",
		},
		{
			name:      "rc tag injected",
			version:   "v1.0.0-rc.3",
			commit:    "deadbee",
			buildDate: "2026-12-31T23:59:59Z",
			wantSub:   "netsite v1.0.0-rc.3 (commit deadbee, built 2026-12-31T23:59:59Z)",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			Version, Commit, BuildDate = tc.version, tc.commit, tc.buildDate
			if got := String(); got != tc.wantSub {
				t.Errorf("String() = %q; want %q", got, tc.wantSub)
			}
		})
	}
}
