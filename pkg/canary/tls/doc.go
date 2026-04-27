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

// Package tls implements the TLS canary Runner.
//
// What: a Runner that performs a TLS handshake against host:port,
// records DNS + connect + handshake timings, and emits cert metadata
// (planned: JA3/JA4/JARM fingerprints once we wire a custom dialer).
//
// How: stdlib `crypto/tls`. The handshake's ConnectionState gives us
// the negotiated cipher suite, protocol version, peer certificates,
// and ALPN — every server-side observable we need short of
// reconstructing the ServerHello bytes. JA3/JA4 of the *client* are
// constants for a given Go runtime; useful for detecting MITM
// alteration but not informative on a per-target basis. The full
// ClientHello-capture path is documented in
// `docs/algorithms/ja3-ja4-fingerprinting.md` and lands when we
// adopt a custom TLS dialer in Phase 1.
//
// Why we ship the TLS Runner now without ClientHello fingerprinting:
// the handshake-level health check (cert validity, protocol version,
// chain) is the headline TLS-canary observation. The fingerprint
// fields exist on Result so the storage column is populated when we
// flip on the dialer; until then those fields are empty strings,
// which the LowCardinality(error_kind) and String columns store
// at near-zero cost.
package tls
