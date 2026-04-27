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

import "fmt"

// Version is the semver-ish release tag (for example "v0.0.1").
// Default "dev" indicates an unsigned, non-release local build.
var Version = "dev"

// Commit is the short Git SHA the binary was built from.
// Default "unknown" indicates the build was not done from a Git checkout
// or the linker did not inject a value.
var Commit = "unknown"

// BuildDate is the UTC RFC3339 timestamp of the build.
// Default "unknown" indicates the linker did not inject a value.
var BuildDate = "unknown"

// String returns a single-line, human-readable version banner suitable
// for `ns version` output and HTTP `X-NetSite-Version` headers.
func String() string {
	return fmt.Sprintf("netsite %s (commit %s, built %s)", Version, Commit, BuildDate)
}
