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

// Package version exposes build-time metadata for the NetSite binaries.
//
// The exported variables are intentionally writable so the Go linker can
// populate them via -ldflags at build time. The Makefile injects values;
// `go run` and unsigned local builds fall back to the defaults defined
// here.
package version
