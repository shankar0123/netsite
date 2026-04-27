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

// Package http implements the HTTP canary Runner.
//
// What: GET (or configurable verb) the target URL and record DNS,
// connect, TLS, TTFB, and total wall-clock timings, plus the response
// status code.
//
// How: stdlib `net/http` + `net/http/httptrace`. httptrace is the
// only stdlib mechanism that exposes per-phase timings; using it
// lets us avoid taking on a heavier dependency just to measure
// what's already in the standard library.
//
// Why one Runner type rather than a free function: the Runner
// holds the http.Client (with shared transport, timeouts, optional
// TLS config) so successive Run calls reuse connections where
// keep-alives apply. POPs canary the same target every 30s; a fresh
// Client per call would defeat keep-alive entirely.
package http

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// Config carries protocol-specific options that override Runner-wide
// defaults for a single Test. Embed this on a Test.Config to use.
type Config struct {
	// Method is the HTTP verb. Default GET.
	Method string
	// ExpectedStatus is the status range that counts as success.
	// Empty means "2xx". Use "200" for exact match, "200-299" for a
	// range. Phase 1 will support multiple ranges.
	ExpectedStatus string
}

// Runner is the HTTP canary Runner.
type Runner struct {
	// Client is the shared HTTP client. The zero value uses
	// http.DefaultClient; production callers should construct a
	// client with sensible Transport timeouts and pass it here.
	Client *http.Client
	// PopID is recorded into every Result this Runner produces.
	PopID string
}

// New constructs a Runner with default HTTP client tuned for canary
// workloads.
func New(popID string) *Runner {
	return &Runner{
		PopID: popID,
		Client: &http.Client{
			Transport: &http.Transport{
				// Disable connection pooling across canary instances
				// to keep timing measurements clean. We can opt-in
				// to keep-alive via a Phase 1 config knob.
				DisableKeepAlives: true,
			},
			// We do NOT set http.Client.Timeout here; the per-Test
			// timeout flows through the request context inside Run.
		},
	}
}

// Run executes a GET (or configured method) against t.Target and
// fills the timing breakdown.
func (r *Runner) Run(ctx context.Context, t canary.Test) canary.Result {
	res := canary.Result{
		TestID:     t.ID,
		TenantID:   t.TenantID,
		PopID:      r.PopID,
		ObservedAt: time.Now().UTC(),
	}

	rawURL := strings.TrimSpace(t.Target)
	if rawURL == "" {
		res.ErrorKind = canary.ErrorKindBadConfig
		return res
	}
	if _, err := url.Parse(rawURL); err != nil {
		res.ErrorKind = canary.ErrorKindBadConfig
		return res
	}

	method := http.MethodGet
	expectedStatus := ""
	if cfg, ok := t.Config.(Config); ok {
		if cfg.Method != "" {
			method = cfg.Method
		}
		expectedStatus = cfg.ExpectedStatus
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Per-phase timestamps tracked via httptrace. We use absolute
	// timestamps then subtract pairwise — that way a missing event
	// (e.g. no TLS) leaves the relevant duration at the zero value
	// without our own state machine.
	var (
		dnsStart, dnsDone   time.Time
		connStart, connDone time.Time
		tlsStart, tlsDone   time.Time
		gotFirstByte        time.Time
	)

	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { connDone = time.Now() },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { tlsDone = time.Now() },
		GotFirstResponseByte: func() { gotFirstByte = time.Now() },
	}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(cctx, trace), method, rawURL, nil)
	if err != nil {
		res.ErrorKind = canary.ErrorKindHTTPRequest
		return res
	}

	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}

	start := time.Now()
	resp, err := client.Do(req)
	end := time.Now()

	res.LatencyMs = milliseconds(end.Sub(start))

	if !dnsStart.IsZero() && !dnsDone.IsZero() {
		res.DNSMs = milliseconds(dnsDone.Sub(dnsStart))
	}
	if !connStart.IsZero() && !connDone.IsZero() {
		res.ConnectMs = milliseconds(connDone.Sub(connStart))
	}
	if !tlsStart.IsZero() && !tlsDone.IsZero() {
		res.TLSMs = milliseconds(tlsDone.Sub(tlsStart))
	}
	if !gotFirstByte.IsZero() {
		res.TTFBMs = milliseconds(gotFirstByte.Sub(start))
	}

	if err != nil {
		res.ErrorKind = classifyHTTPError(err, cctx.Err())
		return res
	}
	defer func() { _ = resp.Body.Close() }()

	res.StatusCode = uint16(resp.StatusCode) //nolint:gosec // status codes fit in uint16
	if !statusInRange(resp.StatusCode, expectedStatus) {
		res.ErrorKind = canary.ErrorKindHTTPStatus
	}
	return res
}

// classifyHTTPError maps a transport-layer error into a canonical
// canary error label.
func classifyHTTPError(err, ctxErr error) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return canary.ErrorKindTimeout
	}
	if isTLSError(err) {
		return canary.ErrorKindTLSHandshk
	}
	return canary.ErrorKindConnect
}

// isTLSError reports whether err looks like a TLS handshake failure.
// We avoid importing crypto/tls just for the type assertion; a string
// match on "tls" + "handshake" is good enough at this layer because
// the surrounding error chain wraps the same words.
func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "tls") && strings.Contains(s, "handshake")
}

// statusInRange reports whether status satisfies expectedStatus.
// expectedStatus formats:
//   - "" (empty) → 2xx (200..299).
//   - "200"      → exact match.
//   - "200-299"  → range, inclusive.
func statusInRange(status int, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return status >= 200 && status < 300
	}
	if dash := strings.IndexByte(expected, '-'); dash >= 0 {
		lo := atoi(expected[:dash])
		hi := atoi(expected[dash+1:])
		if lo <= 0 || hi <= 0 {
			return false
		}
		return status >= lo && status <= hi
	}
	return status == atoi(expected)
}

// atoi is strconv.Atoi but returns 0 on parse error to keep
// statusInRange branchless. This is intentional: a malformed
// ExpectedStatus surfaces as "no status matches" rather than panicking.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// milliseconds converts a duration to a float32 millisecond value.
func milliseconds(d time.Duration) float32 {
	return float32(d.Seconds() * 1000)
}
