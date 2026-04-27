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

package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// Runner is the DNS canary Runner. The zero value resolves via the
// system resolver; set Resolver to override.
type Runner struct {
	// Resolver is the resolver used for lookups. Nil means use the
	// stdlib default (which honours /etc/resolv.conf on Unix).
	Resolver *net.Resolver
	// PopID is recorded into every Result this Runner produces.
	PopID string
}

// New constructs a Runner with the given POP id. Equivalent to
// `&Runner{PopID: popID}`; provided for symmetry with the other
// protocol packages.
func New(popID string) *Runner {
	return &Runner{PopID: popID}
}

// Run resolves t.Target as a hostname and records the elapsed time.
// On success Result.LatencyMs and Result.DNSMs are set to the same
// value (DNS-only canary; nothing happens after resolution).
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

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolver := r.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	start := time.Now()
	_, err := resolver.LookupHost(cctx, target)
	elapsed := time.Since(start)

	res.LatencyMs = milliseconds(elapsed)
	res.DNSMs = res.LatencyMs

	if err != nil {
		res.ErrorKind = classifyDNSError(err, cctx.Err())
	}
	return res
}

// classifyDNSError maps an error from net.Resolver into a canonical
// canary error label. Timeouts collapse to ErrorKindTimeout; anything
// else becomes ErrorKindDNSResolve so the LowCardinality(error_kind)
// column stays small.
func classifyDNSError(err, ctxErr error) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return canary.ErrorKindTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsTimeout {
		return canary.ErrorKindTimeout
	}
	return canary.ErrorKindDNSResolve
}

// milliseconds converts a duration to a float32 millisecond value
// suitable for the ClickHouse Float32 columns in canary_results.
func milliseconds(d time.Duration) float32 {
	return float32(d.Seconds() * 1000)
}
