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

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// What: helpers that propagate W3C Trace Context across NATS messages
// and emit a producer span on publish + a consumer span on receive.
//
// How: PublishWithTrace serializes the context's trace metadata into
// the message Header. ConsumeMessage extracts those headers back into
// a fresh context for handlers, so a span started before publish stays
// linked to spans created during consume.
//
// Why span-linked traces across the bus: the most common NetSite
// debugging question is "where did this canary result/BGP event start
// and what touched it." Without trace-context propagation through
// JetStream, every consumer would be a fresh root span and the answer
// is "nowhere visible." This file is small, but the operational value
// is large.

// PublishWithTrace publishes msg on the given JetStream context,
// emitting a producer span and injecting trace context into the
// message header. Pass an existing context that already contains a
// parent span; the producer span will be a child of that.
//
// On error, the error is recorded on the span and surfaced to the
// caller; the span is closed regardless.
func PublishWithTrace(ctx context.Context, js nats.JetStreamContext, subject string, data []byte) (*nats.PubAck, error) {
	tracer := otel.Tracer("github.com/shankar0123/netsite/nats")
	ctx, span := tracer.Start(ctx, "nats.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination", subject),
		),
	)
	defer span.End()

	// Inject trace headers into a single-value MapCarrier, then copy
	// onto the multi-value nats.Header. The W3C trace-context headers
	// (traceparent, tracestate, baggage) are single-value by spec, so
	// the lossless round-trip is safe.
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}
	for k, v := range carrier {
		msg.Header.Set(k, v)
	}

	ack, err := js.PublishMsg(msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return ack, err
}

// ConsumeContext returns a context whose trace state is initialised
// from msg's headers. Use it as the root context for consumer-side
// handlers so the consumer span links back to the producer's trace.
//
//	for msg := range ch {
//	    ctx := otel.ConsumeContext(context.Background(), msg)
//	    handle(ctx, msg)
//	}
func ConsumeContext(parent context.Context, msg *nats.Msg) context.Context {
	if msg == nil || msg.Header == nil {
		return parent
	}
	carrier := propagation.MapCarrier{}
	for k, vs := range msg.Header {
		if len(vs) > 0 {
			carrier[k] = vs[0]
		}
	}
	return otel.GetTextMapPropagator().Extract(parent, carrier)
}
