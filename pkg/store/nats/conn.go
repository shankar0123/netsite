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
	"time"

	"github.com/nats-io/nats.go"
)

// ErrEmptyURL is returned by Connect when called with an empty URL.
var ErrEmptyURL = errors.New("nats: empty URL")

// Connect dials the NATS server at url with NetSite's standard client
// options: infinite reconnects, 5-second reconnect wait, name set to
// the caller-supplied clientName for server-side observability.
//
// The url follows nats.go's standard form, for example:
//
//	nats://user:pass@host:4222
//	tls://host:4222
//
// Multiple comma-separated URLs are supported for HA: the client
// rotates through them on reconnect.
//
// The returned *nats.Conn is safe for concurrent use. Callers must
// call Close() at shutdown so queued messages are flushed and the
// connection is cleanly drained.
func Connect(url, clientName string) (*nats.Conn, error) {
	if url == "" {
		return nil, ErrEmptyURL
	}

	opts := []nats.Option{
		nats.Name(clientName),
		// MaxReconnects(-1) keeps the connection trying forever. Edge
		// POPs publish from networks where transient outages are the
		// norm; a finite cap converts every long outage into a
		// require-restart event.
		nats.MaxReconnects(-1),
		nats.ReconnectWait(5 * time.Second),
		// PingInterval + MaxPingsOut detect dead servers without
		// waiting for the kernel TCP timeout.
		nats.PingInterval(20 * time.Second),
		nats.MaxPingsOutstanding(2),
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect %s: %w", url, err)
	}
	return nc, nil
}

// JetStream returns a JetStream context bound to nc with NetSite's
// standard publish-side defaults (5-second publish ACK timeout). The
// returned context is safe for concurrent use.
//
// Why a separate constructor: nats.Conn is the wire-level connection;
// the JetStream context is a higher-level API that adds delivery
// guarantees, stream discovery, and per-publish acknowledgement
// configuration on top. Most NetSite call sites want JetStream; a few
// (low-latency RUM ingestion in Phase 3) may use core NATS pub/sub.
func JetStream(nc *nats.Conn) (nats.JetStreamContext, error) {
	if nc == nil {
		return nil, fmt.Errorf("nats: JetStream called with nil conn")
	}
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	return js, nil
}
