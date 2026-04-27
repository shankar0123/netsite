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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// What: a Notifier ships BurnEvent records to whatever incident-
// management surface the operator wired up. The interface is small
// enough that PagerDuty, Slack, OpsGenie, and bespoke webhook
// integrations all fit behind it.
//
// How: we ship a webhook implementation today (POSTs JSON-encoded
// BurnEvents) and a no-op implementation that just logs. Operators
// pick per-SLO via slos.notifier_url; an empty URL means "log only".
//
// Why per-SLO URLs (not a global webhook): some teams route different
// SLOs to different on-call rotations. Per-SLO URLs let an operator
// decide that "control plane availability" pages the platform team
// while "BGP swing detection" pages the network team, all without a
// reverse proxy or a routing rule outside the SLO definition.

// Notifier is the publish-side interface. Implementations must be
// safe to call concurrently from the evaluator's goroutine pool.
type Notifier interface {
	// Notify ships ev to the destination. Implementations should
	// return an error rather than panic; the evaluator logs and
	// proceeds rather than abort the whole tick.
	Notify(ctx context.Context, ev BurnEvent) error
}

// LogNotifier is the default — emits a structured slog record per
// burn event. Useful for development and as a fallback when an
// SLO has no webhook configured.
type LogNotifier struct {
	Logger *slog.Logger
}

// Notify implements Notifier. The slog level is Warn so default
// log filters surface burn events even when the controlplane runs
// at Info.
func (n LogNotifier) Notify(_ context.Context, ev BurnEvent) error {
	l := n.Logger
	if l == nil {
		l = slog.Default()
	}
	l.Warn("slo burn",
		slog.String("slo_id", ev.SLOID),
		slog.String("slo_name", ev.SLOName),
		slog.String("tenant_id", ev.TenantID),
		slog.String("severity", string(ev.Severity)),
		slog.Float64("burn_rate", ev.BurnRate),
		slog.Float64("threshold", ev.Threshold),
		slog.Float64("sli_value", ev.SLIValue),
		slog.Duration("long_window", ev.LongWindow),
		slog.Time("occurred_at", ev.OccurredAt),
	)
	return nil
}

// WebhookNotifier POSTs the BurnEvent as JSON to URL. The receiver
// is expected to return any 2xx; any other status (or transport
// error) surfaces as an error to the evaluator, which logs but
// does not retry. JetStream-style retry semantics for alerts are
// a Phase 5 concern; today's webhook is best-effort.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// NewWebhookNotifier returns a WebhookNotifier with a sane default
// http.Client (5s timeout). Pass a custom client when the receiver
// needs auth or longer timeouts.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:    url,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify implements Notifier.
func (n *WebhookNotifier) Notify(ctx context.Context, ev BurnEvent) error {
	if n.URL == "" {
		return nil
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("slo webhook: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slo webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "netsite-slo-notifier/0.0.7")
	client := n.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("slo webhook: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slo webhook: non-2xx status %d", resp.StatusCode)
	}
	return nil
}

// MultiplexedNotifier picks a notifier per-SLO. The default is
// LogNotifier; an SLO with a NotifierURL gets a WebhookNotifier
// constructed on the fly. Constructed once per evaluator and
// reused; concurrent-safe.
type MultiplexedNotifier struct {
	Default Notifier
}

// Notify dispatches based on whether the BurnEvent originated from
// an SLO with a NotifierURL set. Callers populate ev with whatever
// fields they want; the routing decision happens in the evaluator
// before Notify, which is why MultiplexedNotifier just forwards.
//
// We keep the type for symmetry — future expansion (per-tenant
// override, per-severity routing) lands here without rewiring the
// evaluator.
func (m MultiplexedNotifier) Notify(ctx context.Context, ev BurnEvent) error {
	return m.Default.Notify(ctx, ev)
}
