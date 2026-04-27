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
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config controls how Setup wires up tracing and metrics.
//
// All fields are also exposed via NETSITE_OTEL_* environment variables;
// callers typically use ConfigFromEnv and override per-binary fields.
type Config struct {
	// Enabled gates the entire setup. When false, Setup returns no-op
	// providers and a no-op shutdown — callers run unchanged.
	Enabled bool

	// OTLPEndpoint is the host:port of the OTLP gRPC collector.
	// Default: localhost:4317 (matches the dev compose stack).
	OTLPEndpoint string

	// Insecure controls whether the gRPC connection skips TLS. True is
	// appropriate for local dev (compose stack) and intra-cluster
	// connections; production cross-cluster setups should set false.
	Insecure bool

	// SamplingRatio is the head-based sampling ratio for traces in the
	// 0.0..1.0 range. Parent-based: a span with a sampled parent stays
	// sampled regardless of this ratio. Default 0.01 (1%).
	SamplingRatio float64

	// ServiceName ends up as the resource.Service.Name attribute on
	// every emitted span and metric. Required; Setup returns an error
	// when empty.
	ServiceName string

	// ServiceVersion ends up as resource.Service.Version. Typically the
	// linker-injected pkg/version.Version value.
	ServiceVersion string
}

// ErrEmptyServiceName is returned by Setup when Config.ServiceName is
// empty. Operators rely on the service.name attribute to identify
// emitted spans; an empty name produces blank graphs that look like
// data loss.
var ErrEmptyServiceName = errors.New("otel: empty service name")

// ConfigFromEnv reads the NETSITE_OTEL_* environment variables and
// returns a Config with sensible defaults filled in. Missing or
// malformed variables fall back to defaults rather than aborting; the
// goal is to produce a config that always boots, even with a partial
// environment.
//
// Variables (defaults in parentheses):
//
//	NETSITE_OTEL_ENABLED          (true)
//	NETSITE_OTEL_OTLP_ENDPOINT    (localhost:4317)
//	NETSITE_OTEL_INSECURE         (true)
//	NETSITE_OTEL_SAMPLING_RATIO   (0.01)
//	NETSITE_OTEL_SERVICE_NAME     (caller's binary name; passed in)
//	NETSITE_OTEL_SERVICE_VERSION  (passed in)
func ConfigFromEnv(serviceName, serviceVersion string) Config {
	return Config{
		Enabled:        envBool("NETSITE_OTEL_ENABLED", true),
		OTLPEndpoint:   envString("NETSITE_OTEL_OTLP_ENDPOINT", "localhost:4317"),
		Insecure:       envBool("NETSITE_OTEL_INSECURE", true),
		SamplingRatio:  envFloat("NETSITE_OTEL_SAMPLING_RATIO", 0.01),
		ServiceName:    envString("NETSITE_OTEL_SERVICE_NAME", serviceName),
		ServiceVersion: envString("NETSITE_OTEL_SERVICE_VERSION", serviceVersion),
	}
}

// Shutdown is the function returned by Setup. Callers should defer it
// at process exit (with a bounded context) so in-flight spans and
// metrics flush before the process terminates.
type Shutdown func(context.Context) error

// noopShutdown is what Setup returns when telemetry is disabled.
func noopShutdown(context.Context) error { return nil }

// Setup configures the global OpenTelemetry providers (tracer, meter,
// propagator) according to cfg and returns a Shutdown function. When
// cfg.Enabled is false, Setup is a no-op and returns noopShutdown so
// callers do not need to branch on the enabled flag at use sites.
//
// Setup is safe to call exactly once per process; calling it twice
// replaces the global providers, which is fine for tests but a smell
// in production code.
func Setup(ctx context.Context, cfg Config) (Shutdown, error) {
	if !cfg.Enabled {
		return noopShutdown, nil
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return nil, ErrEmptyServiceName
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		resource.WithProcess(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracegrpc.WithInsecure())
	}
	traceExp, err := otlptracegrpc.New(ctx, traceOpts...)
	if err != nil {
		return nil, fmt.Errorf("otel: trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(cfg.SamplingRatio),
		)),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	metricOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		metricOpts = append(metricOpts, otlpmetricgrpc.WithInsecure())
	}
	metricExp, err := otlpmetricgrpc.New(ctx, metricOpts...)
	if err != nil {
		// Trace pipeline is already up; tear it down so the caller
		// does not leak a goroutine when reporting the metrics-side
		// failure.
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("otel: metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExp,
			sdkmetric.WithInterval(30*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	shutdown := func(ctx context.Context) error {
		var errs []error
		if e := tp.Shutdown(ctx); e != nil {
			errs = append(errs, fmt.Errorf("trace shutdown: %w", e))
		}
		if e := mp.Shutdown(ctx); e != nil {
			errs = append(errs, fmt.Errorf("meter shutdown: %w", e))
		}
		return errors.Join(errs...)
	}
	return shutdown, nil
}

// envString returns the value of key, falling back to def when the
// variable is unset or empty. Empty-as-default matches what operators
// expect when they shell-out an unused config knob with `export VAR=`.
func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envBool returns the parsed value of key, falling back to def when
// the variable is unset, empty, or unparseable.
func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// envFloat returns the parsed value of key, falling back to def when
// the variable is unset, empty, or unparseable.
func envFloat(key string, def float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
