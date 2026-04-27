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
	"strings"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// What: mesh canary mode. Given the local POP id, the local
// tenant id, and a roster of other POPs, generate one HTTP test per
// other POP that targets that POP's health URL. Three POPs running
// mesh therefore produce a 3×3 result matrix where the diagonal is
// each POP measuring everyone except itself.
//
// How: a stateless function over the roster. The output is plain
// canary.Test values; the scheduler treats them like any other test.
// Because the tests are derived from config-side data, not control-
// plane API calls, mesh works the same way in air-gap deployments.
//
// Why mesh in Phase 0: the Phase 0 exit gate calls for "3 POPs
// deployed". Mesh canaries are the cheapest way to demonstrate POP-
// to-POP reachability across that fleet without any external
// targets — operators can run the demo against three local Docker
// containers and the dashboards still light up.

// MeshPeer describes a peer POP this POP should test against.
type MeshPeer struct {
	// ID is the peer's pop_id (pop-<slug>). Skipped if it equals our
	// own id — a POP testing itself adds no signal.
	ID string `yaml:"id"`
	// HealthURL is the full URL of the peer POP's /v1/health (or
	// any URL that returns 2xx when the peer is up).
	HealthURL string `yaml:"health_url"`
}

// MeshConfig is the YAML block under `mesh:` in pop.yaml.
type MeshConfig struct {
	// TenantID owns the generated mesh tests. Defaults to the test's
	// tenant when set per-test elsewhere; here we require it because
	// mesh tests are not declared in the catalog.
	TenantID string `yaml:"tenant_id"`
	// Interval is how often each generated mesh test fires. Default
	// 30s.
	Interval string `yaml:"interval"`
	// Timeout bounds a single mesh-test execution. Default 5s.
	Timeout string `yaml:"timeout"`
	// Peers is the roster — every other POP this POP should ping.
	Peers []MeshPeer `yaml:"peers"`
}

// GenerateMeshTests returns a list of canary.Test values, one per
// MeshConfig.Peers entry whose ID differs from selfID. Returns nil
// if cfg is empty or has no peers other than self.
//
// Test IDs are deterministic — "tst-mesh-<self>-<peer>" — so an
// operator can correlate a row in canary_results to the source +
// destination pair without consulting a separate registry.
func GenerateMeshTests(cfg MeshConfig, selfID string) ([]canary.Test, error) {
	if len(cfg.Peers) == 0 {
		return nil, nil
	}
	tenant := strings.TrimSpace(cfg.TenantID)
	if tenant == "" {
		tenant = "tnt-default"
	}

	interval, err := parseDuration(cfg.Interval, 30*time.Second)
	if err != nil {
		return nil, err
	}
	timeout, err := parseDuration(cfg.Timeout, 5*time.Second)
	if err != nil {
		return nil, err
	}

	out := make([]canary.Test, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p.ID == "" || p.HealthURL == "" {
			continue
		}
		if p.ID == selfID {
			continue
		}
		out = append(out, canary.Test{
			ID:       "tst-mesh-" + selfID + "-" + p.ID,
			TenantID: tenant,
			Kind:     canary.KindHTTP,
			Target:   p.HealthURL,
			Interval: interval,
			Timeout:  timeout,
		})
	}
	return out, nil
}
