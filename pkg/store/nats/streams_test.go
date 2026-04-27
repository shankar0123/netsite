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

//go:build integration

// What: integration tests for EnsureStream / EnsureConsumer against a
// real NATS JetStream server. Asserts create-on-missing, no-op on
// matching, update-on-drift, and pub/sub round-trip.
//
// How: gated behind the `integration` build tag; uses
// testcontainers-go/modules/nats with JetStream enabled.
//
// Why a real server (not nats-server in-process): the in-process server
// is fine for some uses but the testcontainers form mirrors the
// configuration matrix CI runs on a real cluster more closely.
package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

// startNATS returns a running JetStream-enabled NATS server and the
// URL that reaches it. Cleanup is registered with t.Cleanup.
func startNATS(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := tcnats.Run(ctx,
		"nats:2.10-alpine",
		tcnats.WithArgument("jetstream", ""),
	)
	if err != nil {
		t.Fatalf("start nats: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("nats terminate: %v", err)
		}
	})

	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("nats url: %v", err)
	}
	_ = testcontainers.ContainerCustomizer(nil) // tolerate the import
	return url
}

// TestEnsureStream_CreateThenNoOp asserts EnsureStream creates a stream
// on first call and is a no-op on second call with identical config.
func TestEnsureStream_CreateThenNoOp(t *testing.T) {
	url := startNATS(t)
	nc, err := Connect(url, "test")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()
	js, err := JetStream(nc)
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	cfg := &StreamConfig{
		Name:     "NETSITE_TEST",
		Subjects: []string{"netsite.test.>"},
		Storage:  nats.FileStorage,
		MaxAge:   24 * time.Hour,
	}

	// Create.
	got, err := EnsureStream(js, cfg)
	if err != nil {
		t.Fatalf("EnsureStream first: %v", err)
	}
	if got.Config.Name != cfg.Name {
		t.Errorf("created stream name = %q; want %q", got.Config.Name, cfg.Name)
	}

	// No-op.
	got2, err := EnsureStream(js, cfg)
	if err != nil {
		t.Fatalf("EnsureStream second: %v", err)
	}
	if got2.Created != got.Created {
		t.Errorf("second EnsureStream changed Created timestamp; want no-op")
	}
}

// TestEnsureStream_UpdateOnDrift asserts EnsureStream pushes an update
// when the desired config differs from the live config.
func TestEnsureStream_UpdateOnDrift(t *testing.T) {
	url := startNATS(t)
	nc, err := Connect(url, "test")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()
	js, err := JetStream(nc)
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	cfg := &StreamConfig{
		Name:     "NETSITE_TEST",
		Subjects: []string{"netsite.test.>"},
		Storage:  nats.FileStorage,
		MaxAge:   24 * time.Hour,
	}
	if _, err := EnsureStream(js, cfg); err != nil {
		t.Fatalf("EnsureStream first: %v", err)
	}

	// Drift: change retention window.
	cfg.MaxAge = 7 * 24 * time.Hour
	got, err := EnsureStream(js, cfg)
	if err != nil {
		t.Fatalf("EnsureStream second (drift): %v", err)
	}
	if got.Config.MaxAge != cfg.MaxAge {
		t.Errorf("MaxAge after update = %v; want %v", got.Config.MaxAge, cfg.MaxAge)
	}
}

// TestPubSub_RoundTrip is a thin smoke test that JetStream publish +
// pull-subscribe round-trips a message through the helpers.
func TestPubSub_RoundTrip(t *testing.T) {
	url := startNATS(t)
	nc, err := Connect(url, "test")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()
	js, err := JetStream(nc)
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}

	if _, err := EnsureStream(js, &StreamConfig{
		Name:     "NETSITE_RT",
		Subjects: []string{"netsite.rt.>"},
		Storage:  nats.FileStorage,
	}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	if _, err := js.Publish("netsite.rt.hello", []byte("ping")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if _, err := EnsureConsumer(js, "NETSITE_RT", &nats.ConsumerConfig{
		Durable:       "test-consumer",
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: "netsite.rt.>",
	}); err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}

	sub, err := js.PullSubscribe("netsite.rt.>", "test-consumer")
	if err != nil {
		t.Fatalf("PullSubscribe: %v", err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d msgs; want 1", len(msgs))
	}
	if string(msgs[0].Data) != "ping" {
		t.Errorf("msg = %q; want %q", msgs[0].Data, "ping")
	}
	if err := msgs[0].Ack(); err != nil {
		t.Errorf("ack: %v", err)
	}
}
