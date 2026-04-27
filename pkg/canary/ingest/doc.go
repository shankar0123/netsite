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

// Package ingest is the controlplane-side consumer that turns
// canary.Result messages on JetStream into rows in the ClickHouse
// `canary_results` table.
//
// What: a long-lived goroutine that pulls from a durable JetStream
// consumer on the NETSITE_CANARY_RESULTS stream, decodes each
// message as a canary.Result, and inserts it into ClickHouse. Acks
// on a successful insert; NAKs on a transient error so JetStream
// redelivers.
//
// How: the Consumer struct owns the JetStream subscription and the
// ClickHouse connection. Run() blocks until ctx is canceled, then
// drains the in-flight fetch with a brief deadline before returning.
// Errors during decode or insert are logged with the message subject
// and (where possible) the test_id so operators can correlate; the
// loop continues so a single bad message does not stop ingestion.
//
// Why pull-subscribe (not push): pull lets the consumer apply back-
// pressure naturally — if ClickHouse is slow, we fetch fewer
// messages. Push delivery would buffer messages in nats.go's local
// queue and either grow unbounded or drop, depending on options. Pull
// is the JetStream-recommended pattern for at-least-once durable
// consumers and matches the operator-facing semantics ("messages
// flow through here exactly as ClickHouse can absorb them").
package ingest
