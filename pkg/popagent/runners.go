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

package popagent

import (
	"github.com/shankar0123/netsite/pkg/canary"
	dnscanary "github.com/shankar0123/netsite/pkg/canary/dns"
	httpcanary "github.com/shankar0123/netsite/pkg/canary/http"
	tlscanary "github.com/shankar0123/netsite/pkg/canary/tls"
)

// RunnerRegistry maps Kind → Runner. Implements Runners. The default
// registry returned by DefaultRunners includes DNS, HTTP, and TLS;
// tests construct ad-hoc registries with stubs.
type RunnerRegistry struct {
	byKind map[canary.Kind]canary.Runner
}

// NewRunnerRegistry returns an empty registry. Use Register to add
// kinds.
func NewRunnerRegistry() *RunnerRegistry {
	return &RunnerRegistry{byKind: map[canary.Kind]canary.Runner{}}
}

// Register associates a Runner with a Kind. Overwrites any existing
// registration for that Kind.
func (r *RunnerRegistry) Register(kind canary.Kind, runner canary.Runner) {
	r.byKind[kind] = runner
}

// RunnerFor implements the Runners interface.
func (r *RunnerRegistry) RunnerFor(kind canary.Kind) (canary.Runner, bool) {
	run, ok := r.byKind[kind]
	return run, ok
}

// DefaultRunners returns a registry pre-populated with the canary
// protocol implementations enabled in v0.0.5: DNS, HTTP, TLS.
func DefaultRunners(popID string) *RunnerRegistry {
	r := NewRunnerRegistry()
	r.Register(canary.KindDNS, dnscanary.New(popID))
	r.Register(canary.KindHTTP, httpcanary.New(popID))
	r.Register(canary.KindTLS, tlscanary.New(popID))
	return r
}
