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

// Package nats is the NATS JetStream client and stream-management
// surface for NetSite.
//
// What: this package owns the long-lived NATS connection used by the
// control plane and POP/BGP/flow agents to publish and subscribe to
// JetStream subjects (canary results, BGP UPDATE-derived events, flow
// summaries, alert pings).
//
// How: Connect() returns a *nats.Conn configured with infinite
// reconnects and a JetStream context. EnsureStream() declares a stream
// idempotently — if it exists with matching config, it is a no-op; if
// it exists with different config, the stream is updated; if it does
// not exist, it is created.
//
// Why JetStream (not core NATS or Kafka): JetStream gives durable,
// at-least-once delivery with replay and persistence, in a single
// binary, with no extra ops surface. Core NATS is fire-and-forget and
// would lose canary/BGP events on agent restart. Kafka is the
// industry-standard alternative and was rejected in PRD §11 D1 — it
// adds a Zookeeper/KRaft cluster to operate, with no benefit at v1
// scale. Architecture invariant A1 in CLAUDE.md.
//
// Why infinite reconnects: NetSite agents publish from edge POPs over
// best-effort networks. A flapping NATS connection should not require
// a process restart to recover; the client reconnects automatically
// and queues outgoing messages up to the configurable buffer size.
package nats
