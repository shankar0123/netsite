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

// Command ns is the NetSite CLI.
//
// What: a single static binary entry point for operator and admin commands
// against a NetSite control plane. The CLI is the "human surface" parallel
// to the REST/OpenAPI surface that engines consume programmatically.
//
// How: subcommands are registered on a Cobra root command and dispatched
// via Cobra's argument parser. New subcommands live under cmd/ns/cmd/ in
// later phases (auth, tenants, canaries, bgp, flow, pcap). Each subcommand
// owns its flags and calls into pkg/* services through their public APIs.
//
// Why: a single binary is simpler to distribute, sign, and audit than a
// per-command set, and matches the operator expectation set by other Go
// CLIs (kubectl, gh, certctl). Keeping `ns` deliberately thin — no business
// logic in cmd/ — preserves the rule that pkg/ packages are independently
// testable without spinning up the CLI.
//
// This v0 implementation exposes only `ns version`. Subsequent Phase 0
// tasks add `ns seed`, `ns config`, and tenant/canary subcommands.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/shankar0123/netsite/pkg/version"
	"github.com/spf13/cobra"
)

// main is intentionally a one-liner: it delegates to run() so the bulk
// of the entrypoint logic stays unit-testable. Anything more here would
// be uncoverable because tests cannot assert against os.Exit.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the root command tree against the given arguments and IO
// streams and returns a process exit code (0 success, 1 error). Returning
// rather than calling os.Exit makes this function safe to test from
// in-process callers that swap stdout/stderr for buffers.
func run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		// Cobra has already printed the error to the configured stderr
		// because we leave SilenceErrors at its default.
		return 1
	}
	return 0
}

// newRootCmd builds a fresh root command tree on every call.
//
// Why a constructor instead of a package-level var: tests can build a
// fresh tree per test case without leaking flag state between cases, and
// the rest of the codebase never reaches into a global Cobra instance.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ns",
		Short: "NetSite CLI — operator and admin surface",
		Long: `ns is the NetSite CLI. It is the operator and admin surface
parallel to the REST API, suitable for first-run setup, scripted
operations, and one-off queries.

This v0 build exposes only ` + "`ns version`" + `. Subsequent commits add
auth, tenant, canary, BGP, flow, and PCAP subcommands as the matching
engines ship. See PROJECT_STATE.md for current status.`,
		// Don't show the full usage string on every error; Cobra defaults
		// to a verbose error output that's noisy for scripted callers.
		SilenceUsage: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// newVersionCmd returns the `ns version` subcommand. It prints a single
// human-readable line that includes the version tag, short Git SHA, and
// build timestamp. The pkg/version package is the single source of truth;
// the linker injects values via -ldflags at build time (see Makefile).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the NetSite CLI version",
		Long: `Print the NetSite CLI version, the Git commit it was built
from, and the build timestamp. Default values ("dev", "unknown",
"unknown") indicate an unsigned local build; tagged release builds
inject real values via -ldflags.`,
		Run: func(cmd *cobra.Command, args []string) {
			// Use the command's stdout (cmd.OutOrStdout) rather than the
			// process stdout so tests can swap it for a buffer.
			fmt.Fprintln(cmd.OutOrStdout(), version.String())
		},
	}
}
