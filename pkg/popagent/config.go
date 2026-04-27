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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/shankar0123/netsite/pkg/canary"
)

// Config is the on-disk shape of a POP's configuration.
//
// Why a single flat file rather than per-tenant directories: a POP
// physically belongs to one operator. Sharing a POP across tenants
// is a Phase 5 multi-tenancy decision, not a Phase 0 one. A flat
// file with explicit tenant_id on each test models the eventual
// multi-tenant world without forcing the directory layout today.
type Config struct {
	// PopID identifies this POP. Prefixed-TEXT (pop-<slug>).
	PopID string `yaml:"pop_id"`

	// NATSURL is the JetStream client URL the publisher dials.
	NATSURL string `yaml:"nats_url"`

	// Tests is the list of canary checks this POP runs.
	Tests []TestDefinition `yaml:"tests"`

	// Mesh, when present, generates additional HTTP tests against the
	// peers' health URLs. See pkg/popagent/mesh.go.
	Mesh MeshConfig `yaml:"mesh"`
}

// TestDefinition is the YAML representation of a canary.Test plus
// any protocol-specific config knobs.
type TestDefinition struct {
	ID       string `yaml:"id"`
	TenantID string `yaml:"tenant_id"`
	Kind     string `yaml:"kind"`
	Target   string `yaml:"target"`
	Interval string `yaml:"interval"` // duration string, e.g. "30s"
	Timeout  string `yaml:"timeout"`  // duration string, e.g. "5s"

	// HTTP-specific knobs. Optional.
	Method         string `yaml:"method,omitempty"`
	ExpectedStatus string `yaml:"expected_status,omitempty"`
}

// LoadConfig reads, parses, and validates a Config from path.
//
// Returns errors for missing path, malformed YAML, or invalid Test
// definitions (unknown Kind, unparseable durations).
func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("popagent: empty config path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("popagent: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("popagent: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate runs the structural checks LoadConfig applies to every
// loaded Config. Exposed so callers that build Configs in memory
// (tests, future programmatic config) get the same validation.
func (c Config) Validate() error {
	if c.PopID == "" {
		return errors.New("popagent: pop_id is required")
	}
	if c.NATSURL == "" {
		return errors.New("popagent: nats_url is required")
	}
	for i, t := range c.Tests {
		if t.ID == "" {
			return fmt.Errorf("popagent: tests[%d].id is required", i)
		}
		if t.TenantID == "" {
			return fmt.Errorf("popagent: tests[%d].tenant_id is required", i)
		}
		switch canary.Kind(t.Kind) {
		case canary.KindDNS, canary.KindHTTP, canary.KindTLS:
			// ok
		default:
			return fmt.Errorf("popagent: tests[%d].kind = %q not in {dns,http,tls}", i, t.Kind)
		}
		if _, err := parseDuration(t.Interval, 30*time.Second); err != nil {
			return fmt.Errorf("popagent: tests[%d].interval: %w", i, err)
		}
		if _, err := parseDuration(t.Timeout, 5*time.Second); err != nil {
			return fmt.Errorf("popagent: tests[%d].timeout: %w", i, err)
		}
	}
	return nil
}

// parseDuration accepts an empty string (returns def) or a valid
// `time.ParseDuration` string. Anything else is an error.
func parseDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// ToCanaryTest converts a TestDefinition into a canary.Test. Returns
// an error if the durations cannot be parsed; this is duplicated from
// Validate so the caller is not required to call Validate first.
func (td TestDefinition) ToCanaryTest() (canary.Test, error) {
	interval, err := parseDuration(td.Interval, 30*time.Second)
	if err != nil {
		return canary.Test{}, err
	}
	timeout, err := parseDuration(td.Timeout, 5*time.Second)
	if err != nil {
		return canary.Test{}, err
	}
	return canary.Test{
		ID:       td.ID,
		TenantID: td.TenantID,
		Kind:     canary.Kind(td.Kind),
		Target:   td.Target,
		Interval: interval,
		Timeout:  timeout,
	}, nil
}
