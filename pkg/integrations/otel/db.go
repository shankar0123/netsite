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

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// What: a pgx tracer that emits a span per query.
//
// How: implements pgx.QueryTracer. TraceQueryStart begins a span and
// stores it in the returned context; TraceQueryEnd retrieves the span,
// records the SQL as an attribute, marks it errored if the result
// surfaces an error, and ends it.
//
// Why a hand-rolled tracer instead of an external otelpgx wrapper:
// pgx.QueryTracer is a 2-method interface; the wrapper would add a
// dependency for ~50 lines of code. The diligence story is cleaner
// when our instrumentation lives in our repo and references only the
// already-vendored OTel SDK.
//
// Why we record the SQL text as an attribute (instead of redacting):
// NetSite is operator-facing OSS; the operator running the deployment
// is also the consumer of the traces. There is no PII concern that an
// outside party would not already see in their own logs. If a future
// hosted offering changes that calculus, this is the place to switch
// to a parameterized "operation kind" string.

const (
	// pgxQueryAttrSQL is the attribute key NetSite uses for the raw
	// SQL text on pgx query spans. Matches the OpenTelemetry semantic
	// convention `db.statement`.
	pgxQueryAttrSQL = "db.statement"
	// pgxQueryAttrSystem identifies the database family. Constant
	// "postgresql" for everything pgx talks to.
	pgxQueryAttrSystem = "db.system"
)

// PgxTracer is a pgx.QueryTracer implementation that emits one span
// per query against the global TracerProvider.
//
// Use it via pgxpool.Config.ConnConfig.Tracer:
//
//	cfg, _ := pgxpool.ParseConfig(dsn)
//	cfg.ConnConfig.Tracer = otel.NewPgxTracer()
//	pool, _ := pgxpool.NewWithConfig(ctx, cfg)
type PgxTracer struct {
	tracer trace.Tracer
}

// NewPgxTracer returns a pgx.QueryTracer bound to the global
// TracerProvider's tracer. Safe for concurrent use.
func NewPgxTracer() *PgxTracer {
	return &PgxTracer{tracer: otel.Tracer("github.com/shankar0123/netsite/pgx")}
}

// pgxSpanKey is a private context-key type so callers cannot collide
// with our context entries.
type pgxSpanKey struct{}

// TraceQueryStart implements pgx.QueryTracer. It opens a child span on
// the supplied context and returns the context augmented with the
// span pointer.
func (t *PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := t.tracer.Start(ctx, "pgx.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String(pgxQueryAttrSystem, "postgresql"),
			attribute.String(pgxQueryAttrSQL, data.SQL),
		),
	)
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

// TraceQueryEnd implements pgx.QueryTracer. It looks up the span we
// opened in TraceQueryStart, records the error (if any), and ends it.
func (t *PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(pgxSpanKey{}).(trace.Span)
	if !ok || span == nil {
		return
	}
	defer span.End()
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
}
