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

package otel

import (
	"context"
	"errors"
	"testing"
)

// TestConfigFromEnv_Defaults asserts that with no NETSITE_OTEL_*
// variables set (or set to empty), ConfigFromEnv produces the
// documented defaults. A regression here would silently drop
// telemetry on production binaries that boot with a partial env.
//
// Note on test clearing: t.Setenv(k, "") sets the variable to empty
// rather than unsetting it. envString/envBool/envFloat treat empty
// as "use default", so this test exercises both the unset and empty
// paths simultaneously.
func TestConfigFromEnv_Defaults(t *testing.T) {
	for _, k := range []string{
		"NETSITE_OTEL_ENABLED",
		"NETSITE_OTEL_OTLP_ENDPOINT",
		"NETSITE_OTEL_INSECURE",
		"NETSITE_OTEL_SAMPLING_RATIO",
		"NETSITE_OTEL_SERVICE_NAME",
		"NETSITE_OTEL_SERVICE_VERSION",
	} {
		t.Setenv(k, "")
	}

	cfg := ConfigFromEnv("ns-test", "v0.0.0-dev")
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Enabled default", cfg.Enabled, true},
		{"Endpoint default", cfg.OTLPEndpoint, "localhost:4317"},
		{"Insecure default", cfg.Insecure, true},
		{"Ratio default", cfg.SamplingRatio, 0.01},
		{"ServiceName fallback", cfg.ServiceName, "ns-test"},
		{"ServiceVersion fallback", cfg.ServiceVersion, "v0.0.0-dev"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v; want %v", tc.got, tc.want)
			}
		})
	}
}

// TestConfigFromEnv_Overrides asserts each documented env var actually
// overrides the default when set. Catches typos in env-var names.
func TestConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("NETSITE_OTEL_ENABLED", "false")
	t.Setenv("NETSITE_OTEL_OTLP_ENDPOINT", "collector.example:4317")
	t.Setenv("NETSITE_OTEL_INSECURE", "false")
	t.Setenv("NETSITE_OTEL_SAMPLING_RATIO", "0.5")
	t.Setenv("NETSITE_OTEL_SERVICE_NAME", "ns-controlplane")
	t.Setenv("NETSITE_OTEL_SERVICE_VERSION", "v0.1.0")

	cfg := ConfigFromEnv("fallback-name", "fallback-version")
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"Enabled override", cfg.Enabled, false},
		{"Endpoint override", cfg.OTLPEndpoint, "collector.example:4317"},
		{"Insecure override", cfg.Insecure, false},
		{"Ratio override", cfg.SamplingRatio, 0.5},
		{"ServiceName override", cfg.ServiceName, "ns-controlplane"},
		{"ServiceVersion override", cfg.ServiceVersion, "v0.1.0"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v; want %v", tc.got, tc.want)
			}
		})
	}
}

// TestConfigFromEnv_MalformedFallsBack asserts that a malformed env
// var produces the default rather than a crash. Operators sometimes
// ship the wrong type to a config knob; better to boot with defaults
// than refuse to start.
func TestConfigFromEnv_MalformedFallsBack(t *testing.T) {
	t.Setenv("NETSITE_OTEL_ENABLED", "not-a-bool")
	t.Setenv("NETSITE_OTEL_SAMPLING_RATIO", "not-a-float")
	cfg := ConfigFromEnv("ns-test", "")
	if !cfg.Enabled {
		t.Error("malformed bool should fall back to default true")
	}
	if cfg.SamplingRatio != 0.01 {
		t.Errorf("malformed float should fall back to 0.01; got %v", cfg.SamplingRatio)
	}
}

// TestSetup_DisabledIsNoop asserts that Setup with Enabled=false
// returns a non-nil shutdown that itself is a no-op. This is the
// "telemetry is off but everything else still works" path.
func TestSetup_DisabledIsNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Setup(disabled) err: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup(disabled) returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop shutdown returned error: %v", err)
	}
}

// TestSetup_EmptyServiceName asserts the empty-service-name guard
// fires and returns the documented sentinel.
func TestSetup_EmptyServiceName(t *testing.T) {
	_, err := Setup(context.Background(), Config{Enabled: true, ServiceName: ""})
	if !errors.Is(err, ErrEmptyServiceName) {
		t.Errorf("Setup err = %v; want ErrEmptyServiceName", err)
	}
}
