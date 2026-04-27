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

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/nats-io/nats.go"

	"github.com/shankar0123/netsite/pkg/canary"
	natsstore "github.com/shankar0123/netsite/pkg/store/nats"
)

// StreamName is the JetStream stream the consumer attaches to.
// Mirrors the catalog in pkg/store/nats/streams/README.md.
const StreamName = "NETSITE_CANARY_RESULTS"

// SubjectPattern is the wildcard the consumer subscribes to. Matches
// what the POP publisher writes (netsite.canary.results.<test_id>).
const SubjectPattern = "netsite.canary.results.>"

// DurableName is the durable consumer's name. JetStream's at-least-
// once delivery is keyed on (stream, durable_name); using a stable
// name here means a controlplane restart resumes from the right
// position rather than starting over.
const DurableName = "controlplane-ingest"

// Consumer ingests canary results from JetStream into ClickHouse.
type Consumer struct {
	logger *slog.Logger
	js     nats.JetStreamContext
	ch     driver.Conn

	// fetchSize bounds how many messages we ask for per Pull. Larger
	// is better throughput; smaller is more responsive to context
	// cancellation. 64 is a hand-tuned compromise that empirically
	// balances both at v0 scale.
	fetchSize int
	// fetchWait bounds how long Pull blocks waiting for messages.
	fetchWait time.Duration
}

// NewConsumer wires a Consumer with sensible defaults.
func NewConsumer(logger *slog.Logger, js nats.JetStreamContext, ch driver.Conn) *Consumer {
	return &Consumer{
		logger:    logger,
		js:        js,
		ch:        ch,
		fetchSize: 64,
		fetchWait: 2 * time.Second,
	}
}

// EnsureStream declares the canary-results stream if it is not
// already present. Idempotent: matches the apply semantics of
// pkg/store/nats.EnsureStream. The controlplane calls this at boot
// so a fresh cluster works without manual NATS configuration.
func EnsureStream(js nats.JetStreamContext) error {
	_, err := natsstore.EnsureStream(js, &natsstore.StreamConfig{
		Name:     StreamName,
		Subjects: []string{SubjectPattern},
		Storage:  nats.FileStorage,
		MaxAge:   7 * 24 * time.Hour,
	})
	return err
}

// Run subscribes to the canary-results stream and ingests messages
// until ctx is canceled. Returns nil on clean shutdown.
//
// Errors during message handling are logged but never abort the
// loop. A persistent class of errors (e.g. ClickHouse pool exhausted)
// will produce log spam; that's the right signal to operators
// without taking the pipeline down silently.
func (c *Consumer) Run(ctx context.Context) error {
	sub, err := c.js.PullSubscribe(SubjectPattern, DurableName,
		nats.BindStream(StreamName),
		nats.ManualAck(),
	)
	if err != nil {
		return fmt.Errorf("ingest: PullSubscribe: %w", err)
	}
	defer func() { _ = sub.Drain() }()

	c.logger.Info("canary-results consumer running",
		slog.String("stream", StreamName),
		slog.String("durable", DurableName),
	)

	for {
		if ctx.Err() != nil {
			c.logger.Info("canary-results consumer shutting down")
			return nil
		}
		msgs, err := sub.Fetch(c.fetchSize, nats.MaxWait(c.fetchWait))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.Canceled) {
				continue
			}
			c.logger.Warn("Fetch error", slog.Any("err", err))
			continue
		}
		for _, m := range msgs {
			c.handle(ctx, m)
		}
	}
}

// handle decodes one message and inserts the result. Acks on
// success, NAKs on transient error so JetStream redelivers.
//
// We deliberately do not Term (permanently reject) on decode errors
// at the application layer — a malformed message is almost always a
// publisher bug we want operators to notice via redelivery alarms.
// JetStream's MaxDeliver setting is the right backstop; we rely on
// the per-stream default rather than configuring a specific limit.
func (c *Consumer) handle(ctx context.Context, m *nats.Msg) {
	var res canary.Result
	if err := json.Unmarshal(m.Data, &res); err != nil {
		c.logger.Warn("decode failed",
			slog.String("subject", m.Subject), slog.Any("err", err))
		_ = m.Nak()
		return
	}
	if err := c.insert(ctx, res); err != nil {
		c.logger.Warn("clickhouse insert failed",
			slog.String("test_id", res.TestID),
			slog.String("subject", m.Subject),
			slog.Any("err", err))
		_ = m.Nak()
		return
	}
	if err := m.Ack(); err != nil {
		c.logger.Warn("ack failed", slog.Any("err", err))
	}
}

// insert writes one Result into ClickHouse. We use a single-row
// INSERT here for simplicity; the v0.0.6 row volume (one row per
// canary fire) is well within ClickHouse's tolerance for non-batched
// writes. Phase 1 will add a batching writer when the row volume
// crosses the threshold where single-row inserts strain MergeTree
// part counts.
func (c *Consumer) insert(ctx context.Context, r canary.Result) error {
	const q = `INSERT INTO canary_results (
        tenant_id, test_id, pop_id, observed_at,
        latency_ms, dns_ms, connect_ms, tls_ms, ttfb_ms,
        status_code, error_kind, ja3, ja4
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return c.ch.Exec(ctx, q,
		r.TenantID, r.TestID, r.PopID, r.ObservedAt,
		r.LatencyMs, r.DNSMs, r.ConnectMs, r.TLSMs, r.TTFBMs,
		r.StatusCode, r.ErrorKind, r.JA3, r.JA4,
	)
}
