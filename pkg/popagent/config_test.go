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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

const sampleConfig = `
pop_id: pop-test
nats_url: nats://localhost:4222
tests:
  - id: tst-http
    tenant_id: tnt-default
    kind: http
    target: https://example.com/healthz
    interval: 30s
    timeout: 5s
    method: GET
    expected_status: "200-299"
  - id: tst-tls
    tenant_id: tnt-default
    kind: tls
    target: example.com:443
    interval: 60s
`

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pop.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfig_ParsesAndValidates(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PopID != "pop-test" {
		t.Errorf("PopID = %q; want pop-test", cfg.PopID)
	}
	if len(cfg.Tests) != 2 {
		t.Fatalf("Tests len = %d; want 2", len(cfg.Tests))
	}
	if cfg.Tests[0].Kind != string(canary.KindHTTP) {
		t.Errorf("Tests[0].Kind = %q; want http", cfg.Tests[0].Kind)
	}
}

func TestTestDefinition_ToCanaryTest(t *testing.T) {
	td := TestDefinition{
		ID: "tst-1", TenantID: "tnt-x", Kind: "dns",
		Target: "example.com", Interval: "15s", Timeout: "3s",
	}
	got, err := td.ToCanaryTest()
	if err != nil {
		t.Fatalf("ToCanaryTest: %v", err)
	}
	if got.Interval != 15*time.Second {
		t.Errorf("Interval = %v; want 15s", got.Interval)
	}
	if got.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v; want 3s", got.Timeout)
	}
}

func TestConfig_Validate_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{"empty pop_id", "nats_url: nats://x\n", "pop_id"},
		{"empty nats_url", "pop_id: pop-x\n", "nats_url"},
		{"unknown kind", `
pop_id: pop-x
nats_url: nats://x
tests:
  - id: t
    tenant_id: tnt
    kind: bogus
`, "kind"},
		{"bad interval", `
pop_id: pop-x
nats_url: nats://x
tests:
  - id: t
    tenant_id: tnt
    kind: http
    interval: forever
`, "interval"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, tc.yaml)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Errorf("err = %v; want substring %q", err, tc.wantSub)
			}
		})
	}
}

// contains is strings.Contains kept inline so the test file does not
// import strings just for one call.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
