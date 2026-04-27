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

package nats

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// What: idempotent stream and durable-consumer declaration helpers.
//
// How: EnsureStream() looks up a stream by name. If absent, it is
// created. If present with mismatched config, it is updated to match
// the desired config. The semantics mirror Kubernetes' "apply"
// behavior: the desired state is the source of truth.
//
// Why: control-plane and POP processes can boot any number of times
// against the same NATS cluster. Each boot must converge the cluster
// to the desired set of streams without operator intervention and
// without duplicating data on restart. EnsureStream is the boot-time
// "make my streams exist as configured" primitive.

// StreamConfig is a thin re-alias of nats.StreamConfig kept here to
// give callers a stable named type and to leave room for NetSite-
// specific defaults in future. Subject naming convention:
//
//	NETSITE_<DOMAIN>_<EVENT>     stream name (uppercase, underscored)
//	netsite.<domain>.<event>.>   subject pattern (lowercase, dotted)
//
// Domains in v0: canary, bgp, flow, pcap, alerts. New domains require
// updating the catalog in this package's README.
type StreamConfig = nats.StreamConfig

// EnsureStream converges a stream to the desired config. If the stream
// does not exist it is created; if it exists with a different config,
// the stream is updated. Returns the resulting *nats.StreamInfo.
//
// The Name and Subjects fields of cfg are required. Storage defaults
// to FileStorage if unspecified — JetStream's MemoryStorage loses data
// on server restart, which we never want for NetSite's at-least-once
// delivery contract.
func EnsureStream(js nats.JetStreamContext, cfg *StreamConfig) (*nats.StreamInfo, error) {
	if js == nil {
		return nil, errors.New("nats: EnsureStream called with nil JetStreamContext")
	}
	if cfg == nil {
		return nil, errors.New("nats: EnsureStream called with nil StreamConfig")
	}
	if cfg.Name == "" {
		return nil, errors.New("nats: EnsureStream cfg.Name is empty")
	}
	if len(cfg.Subjects) == 0 {
		return nil, fmt.Errorf("nats: EnsureStream cfg.Subjects is empty for %q", cfg.Name)
	}
	if cfg.Storage == 0 {
		// nats.MemoryStorage == 0; FileStorage == 1. Default to file.
		cfg.Storage = nats.FileStorage
	}

	info, err := js.StreamInfo(cfg.Name)
	if err != nil {
		// nats.go reports ErrStreamNotFound for "doesn't exist", which
		// is the create-path. Anything else is a real error.
		if errors.Is(err, nats.ErrStreamNotFound) {
			return js.AddStream(cfg)
		}
		return nil, fmt.Errorf("nats: stream info %q: %w", cfg.Name, err)
	}

	// Stream exists. If the desired config diverges from the live
	// config, push an update; otherwise no-op.
	if streamConfigDiffers(cfg, &info.Config) {
		return js.UpdateStream(cfg)
	}
	return info, nil
}

// streamConfigDiffers compares the fields NetSite actively manages.
// We deliberately do not deep-compare every field of StreamConfig:
// some are server-set (CreatedAt-derived metadata) and would always
// "differ", causing pointless churn.
func streamConfigDiffers(want, have *nats.StreamConfig) bool {
	if want.Storage != have.Storage {
		return true
	}
	if want.Retention != have.Retention {
		return true
	}
	if want.MaxAge != have.MaxAge {
		return true
	}
	if want.MaxBytes != have.MaxBytes {
		return true
	}
	if want.Replicas != have.Replicas && want.Replicas != 0 {
		// A zero want.Replicas means "server default"; don't churn.
		return true
	}
	if !equalStringSlices(want.Subjects, have.Subjects) {
		return true
	}
	return false
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EnsureConsumer converges a durable consumer to the desired config on
// the named stream. Same apply semantics as EnsureStream: create if
// absent, update if drifted, no-op if already matching.
//
// Durable name is required: ephemeral consumers do not survive client
// disconnects and are not what NetSite uses for production agents.
func EnsureConsumer(js nats.JetStreamContext, streamName string, cfg *nats.ConsumerConfig) (*nats.ConsumerInfo, error) {
	if js == nil {
		return nil, errors.New("nats: EnsureConsumer called with nil JetStreamContext")
	}
	if cfg == nil {
		return nil, errors.New("nats: EnsureConsumer called with nil ConsumerConfig")
	}
	if streamName == "" {
		return nil, errors.New("nats: EnsureConsumer streamName is empty")
	}
	if cfg.Durable == "" {
		return nil, errors.New("nats: EnsureConsumer cfg.Durable is required")
	}

	info, err := js.ConsumerInfo(streamName, cfg.Durable)
	if err != nil {
		if errors.Is(err, nats.ErrConsumerNotFound) {
			return js.AddConsumer(streamName, cfg)
		}
		return nil, fmt.Errorf("nats: consumer info %s/%s: %w", streamName, cfg.Durable, err)
	}

	if consumerConfigDiffers(cfg, &info.Config) {
		return js.UpdateConsumer(streamName, cfg)
	}
	return info, nil
}

func consumerConfigDiffers(want, have *nats.ConsumerConfig) bool {
	if want.AckPolicy != have.AckPolicy {
		return true
	}
	if want.AckWait != have.AckWait && want.AckWait != 0 {
		return true
	}
	if want.MaxDeliver != have.MaxDeliver && want.MaxDeliver != 0 {
		return true
	}
	if want.FilterSubject != have.FilterSubject {
		return true
	}
	return false
}
