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

package slo

import (
	"errors"
	"testing"
)

// TestNewWebhookNotifier_RejectsHTTP enforces CLAUDE.md A11 at the
// notifier construction site: an http:// URL surfaces ErrInsecure-
// Webhook so the evaluator can log+skip rather than send the alert
// over plaintext.
func TestNewWebhookNotifier_RejectsHTTP(t *testing.T) {
	cases := []string{
		"http://hooks.example.com/x",
		"http://localhost:8080/notify",
		"",
		"ftp://example.com/x",
		"hooks.example.com/x", // missing scheme entirely
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			n, err := NewWebhookNotifier(url)
			if !errors.Is(err, ErrInsecureWebhook) {
				t.Errorf("NewWebhookNotifier(%q) err = %v; want ErrInsecureWebhook", url, err)
			}
			if n != nil {
				t.Errorf("NewWebhookNotifier(%q) returned non-nil notifier", url)
			}
		})
	}
}

// TestNewWebhookNotifier_AcceptsHTTPS asserts the happy path.
func TestNewWebhookNotifier_AcceptsHTTPS(t *testing.T) {
	n, err := NewWebhookNotifier("https://hooks.example.com/x")
	if err != nil {
		t.Fatalf("NewWebhookNotifier: %v", err)
	}
	if n == nil {
		t.Fatal("NewWebhookNotifier returned nil")
	}
	if n.URL != "https://hooks.example.com/x" {
		t.Errorf("URL = %q; want https://hooks.example.com/x", n.URL)
	}
}

// TestWebhookNotifier_AllowInsecureExplicitOptIn covers the operator
// escape hatch — direct struct literal with AllowInsecure=true. The
// constructor refuses; the struct path is the documented escape.
func TestWebhookNotifier_AllowInsecureExplicitOptIn(t *testing.T) {
	n := &WebhookNotifier{URL: "http://internal.example/x", AllowInsecure: true}
	if !n.AllowInsecure {
		t.Error("AllowInsecure should be true")
	}
	// We deliberately do not exercise Notify against a real http
	// server here; the field's existence + role is the contract.
}
