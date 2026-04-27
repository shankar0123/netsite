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

package canary

import (
	"context"
	"time"
)

// Kind identifies which protocol a Test exercises. Stored as a string
// so the wire format is human-greppable; the Postgres tests-catalog
// table (Phase 1) will declare the same enum via a CHECK constraint.
type Kind string

// Canonical Kind values. Adding a new Kind requires:
//   - a new subpackage under pkg/canary/ implementing Runner.
//   - a wire-up in pkg/popagent/runner.go (when added).
//   - the corresponding CHECK constraint update in tests-catalog.
const (
	KindDNS  Kind = "dns"
	KindHTTP Kind = "http"
	KindTLS  Kind = "tls"
)

// Test is a single check definition. It is the "what to measure"
// record; Runners turn it into a Result.
//
// Why we carry Interval and Timeout on the Test rather than as a
// scheduler-side concern: the scheduler is the orchestrator, but the
// Runner needs Timeout to bound a slow target. Interval is on the
// Test so that the catalog row in Postgres has all the cadence
// information in one place — operators read one row, not two.
type Test struct {
	// ID is the prefixed-TEXT identifier (tst-<short>).
	ID string
	// TenantID owns this test (tnt-<slug>).
	TenantID string
	// Kind selects the protocol Runner.
	Kind Kind
	// Target is interpreted per Kind:
	//   dns:  the question (e.g. "example.com" or "AAAA example.com")
	//   http: the full URL (https://api.example.com/healthz)
	//   tls:  host:port (api.example.com:443)
	Target string
	// Interval is how often the scheduler runs this Test. 30s default.
	Interval time.Duration
	// Timeout bounds a single Test execution. 5s default.
	Timeout time.Duration
	// Config carries protocol-specific options (e.g. HTTP method,
	// expected status, DNS record type). Concrete shape lives in the
	// protocol package; this is intentionally `any` so callers do not
	// import every protocol's config struct.
	Config any
}

// Result is the outcome of one Test execution. Fields are the union
// across protocols; an unfilled field stays zero.
//
// Time fields use float32 milliseconds to match the ClickHouse
// `canary_results` column types defined in
// `pkg/store/clickhouse/schema/0002_canary_results.sql`. Float32 is
// adequate: a millisecond is the smallest meaningful unit at our
// scale and float32 has 7 decimal digits of precision, which is more
// than enough for hours of latency measurements.
type Result struct {
	TestID     string    // mirrors Test.ID
	TenantID   string    // mirrors Test.TenantID
	PopID      string    // POP that ran the test (pop-<slug>)
	ObservedAt time.Time // UTC, set by the Runner at start time
	LatencyMs  float32   // total wall clock for the check
	DNSMs      float32   // DNS resolution time when applicable
	ConnectMs  float32   // TCP connect time when applicable
	TLSMs      float32   // TLS handshake time when applicable
	TTFBMs     float32   // time-to-first-byte for HTTP
	StatusCode uint16    // HTTP status code; 0 for non-HTTP
	ErrorKind  string    // canonical error label, "" on success
	JA3        string    // server-cert / client-hello fingerprint (TLS)
	JA4        string    // newer-format fingerprint (TLS)
}

// Runner executes a single Test and returns a Result. Implementations
// must:
//   - never panic; convert errors into Result.ErrorKind
//   - never block past ctx.Done() or t.Timeout, whichever comes first
//   - populate every timing field they can measure even on partial
//     success (e.g. HTTP TLS handshake succeeded but the GET timed
//     out)
//
// Implementations are concurrency-safe: the POP scheduler may run the
// same Runner against multiple Tests in parallel.
type Runner interface {
	Run(ctx context.Context, t Test) Result
}

// Canonical ErrorKind labels. New labels land here as protocols add
// failure modes; the storage layer's LowCardinality(error_kind)
// column compresses well with a small enum, so we deliberately keep
// the cardinality low.
const (
	ErrorKindNone        = ""
	ErrorKindDNSResolve  = "dns_resolve"
	ErrorKindConnect     = "connect"
	ErrorKindTLSHandshk  = "tls_handshake"
	ErrorKindHTTPStatus  = "http_status"
	ErrorKindHTTPRequest = "http_request"
	ErrorKindTimeout     = "timeout"
	ErrorKindUnknownKind = "unknown_kind"
	ErrorKindBadConfig   = "bad_config"
)
