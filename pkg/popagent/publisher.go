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
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/shankar0123/netsite/pkg/canary"
)

// What: a Result publisher that ships canary.Results to JetStream on
// the NETSITE_CANARY_RESULTS subject pattern documented in
// pkg/store/nats/streams/README.md.
//
// How: marshals the Result to JSON, publishes synchronously, and
// returns any publish error to the caller (which the scheduler logs
// but does not retry — JetStream's at-least-once delivery is good
// enough that a per-publish retry loop here would just hide
// problems).
//
// Why JSON on the wire and not protobuf: NetSite is single-buyer OSS;
// the wire format inside the deployment is ours to choose. JSON is
// trivially debuggable (`nats sub 'netsite.canary.results.>'` shows
// readable rows in the operator's terminal), at the cost of a few
// percent CPU and a few percent network bytes. Protobuf becomes
// worth-it when the publisher rate or the ingestion CPU starts to
// matter (Phase 3, when flow records add 100x the row volume).

// NATSPublisher publishes Results to JetStream.
type NATSPublisher struct {
	js      nats.JetStreamContext
	subject string
}

// SubjectPrefix is the JetStream subject pattern used for canary
// results. The full per-result subject ends with the test ID so
// JetStream-side filtering by Test is one wildcard expansion away.
const SubjectPrefix = "netsite.canary.results"

// NewNATSPublisher constructs a publisher bound to js. The publisher
// publishes to "netsite.canary.results.<test_id>" by default.
func NewNATSPublisher(js nats.JetStreamContext) *NATSPublisher {
	return &NATSPublisher{js: js, subject: SubjectPrefix}
}

// Publish marshals r to JSON and publishes it. Returns an error if
// either the marshal or the JetStream publish fails.
func (p *NATSPublisher) Publish(_ context.Context, r canary.Result) error {
	if p.js == nil {
		return errors.New("popagent: nil JetStreamContext")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("popagent: marshal result: %w", err)
	}
	subject := p.subject + "." + safeSubjectToken(r.TestID)
	if _, err := p.js.Publish(subject, body); err != nil {
		return fmt.Errorf("popagent: publish %s: %w", subject, err)
	}
	return nil
}

// safeSubjectToken sanitizes a string for use as a NATS subject
// token. NATS forbids spaces and the dot/star/wildcard characters in
// tokens. Replace anything not in [a-zA-Z0-9._-] with "_". (The dot
// is allowed but we never want it inside a token; we already split
// on dots at the framing layer.)
func safeSubjectToken(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "_"
	}
	return string(out)
}
