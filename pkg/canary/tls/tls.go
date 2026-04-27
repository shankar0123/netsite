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

package tls

import (
	"context"
	cryptotls "crypto/tls"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// Runner is the TLS canary Runner.
type Runner struct {
	// PopID is recorded into every Result this Runner produces.
	PopID string
	// InsecureSkipVerify disables certificate verification when true.
	// Operators sometimes need to canary an internal endpoint with a
	// private CA they have not yet imported; this flag lets them.
	// Defaults false; production POPs should leave it false.
	InsecureSkipVerify bool
}

// New constructs a Runner with verification enabled.
func New(popID string) *Runner {
	return &Runner{PopID: popID}
}

// Run performs a TLS handshake against t.Target and records timings.
// t.Target is "host:port"; if no port is given, 443 is assumed.
func (r *Runner) Run(ctx context.Context, t canary.Test) canary.Result {
	res := canary.Result{
		TestID:     t.ID,
		TenantID:   t.TenantID,
		PopID:      r.PopID,
		ObservedAt: time.Now().UTC(),
	}

	target := strings.TrimSpace(t.Target)
	if target == "" {
		res.ErrorKind = canary.ErrorKindBadConfig
		return res
	}
	host, port := splitHostPort(target)
	if host == "" {
		res.ErrorKind = canary.ErrorKindBadConfig
		return res
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Resolve via stdlib resolver first so we can attribute DNS time
	// separately from connect time. Without this split we'd attribute
	// all of "before handshake" to ConnectMs, which obscures the
	// surprisingly common DNS-is-slow failure mode.
	dnsStart := time.Now()
	addrs, dnsErr := net.DefaultResolver.LookupHost(cctx, host)
	res.DNSMs = milliseconds(time.Since(dnsStart))
	if dnsErr != nil || len(addrs) == 0 {
		res.LatencyMs = res.DNSMs
		res.ErrorKind = classifyTLSError(dnsErr, cctx.Err(), false)
		return res
	}

	dialer := &net.Dialer{Timeout: timeout}
	connStart := time.Now()
	rawConn, err := dialer.DialContext(cctx, "tcp", net.JoinHostPort(addrs[0], port))
	res.ConnectMs = milliseconds(time.Since(connStart))
	if err != nil {
		res.LatencyMs = res.DNSMs + res.ConnectMs
		res.ErrorKind = classifyTLSError(err, cctx.Err(), false)
		return res
	}
	defer func() { _ = rawConn.Close() }()

	tlsConn := cryptotls.Client(rawConn, &cryptotls.Config{
		ServerName:         host,
		InsecureSkipVerify: r.InsecureSkipVerify, //nolint:gosec // operator-controlled
		MinVersion:         cryptotls.VersionTLS12,
	})

	handshakeStart := time.Now()
	err = tlsConn.HandshakeContext(cctx)
	res.TLSMs = milliseconds(time.Since(handshakeStart))
	res.LatencyMs = res.DNSMs + res.ConnectMs + res.TLSMs
	if err != nil {
		res.ErrorKind = classifyTLSError(err, cctx.Err(), true)
		return res
	}

	// Successful handshake. Future task: extract JA3/JA4 from the
	// connection state's hello fingerprint once we adopt a dialer
	// that exposes the ClientHello bytes. For now leave the
	// fingerprint fields empty.
	_ = tlsConn.ConnectionState()
	return res
}

// splitHostPort parses "host:port" or "host" → (host, port). Default
// port is 443 because the most common TLS canary target is HTTPS.
func splitHostPort(target string) (string, string) {
	host, port, err := net.SplitHostPort(target)
	if err == nil {
		return host, port
	}
	// No port given.
	return target, "443"
}

// classifyTLSError canonicalises errors into the small enum the
// LowCardinality(error_kind) ClickHouse column compresses well.
func classifyTLSError(err, ctxErr error, isHandshake bool) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return canary.ErrorKindTimeout
	}
	if isHandshake {
		return canary.ErrorKindTLSHandshk
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return canary.ErrorKindDNSResolve
	}
	return canary.ErrorKindConnect
}

// milliseconds converts a duration to a float32 millisecond value.
func milliseconds(d time.Duration) float32 {
	return float32(d.Seconds() * 1000)
}
