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

// Package dns implements the DNS canary Runner.
//
// What: a Runner that resolves a hostname against a configurable
// resolver and records DNS lookup time, return-code class, and a
// success/failure verdict.
//
// How: stdlib `net.Resolver`. We deliberately do NOT pull
// `github.com/miekg/dns` for v0; the stdlib resolver is enough for
// A/AAAA + the operator's system resolver. miekg/dns lands in Phase 1
// when we need to query specific record types (TXT, MX, NS, CAA) for
// richer diagnostics — that's a feature, not a Phase 0 requirement.
//
// Why a Resolver field on the Runner rather than always using the
// system resolver: integration tests need to point at a fake DNS
// server. Production POPs in air-gap deployments need to point at
// internal resolvers. A configurable Resolver covers both.
package dns
