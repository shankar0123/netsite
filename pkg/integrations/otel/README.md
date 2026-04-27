# OpenTelemetry foundation

> Source: [`setup.go`](./setup.go) (providers + env config),
> [`http.go`](./http.go) (server-span middleware),
> [`db.go`](./db.go) (pgx tracer),
> [`nats.go`](./nats.go) (publish/consume context propagation).

## What this package does

Every NetSite binary calls `otel.Setup()` early during boot and defers
the returned `Shutdown` at exit. After that:

- `otel.Middleware(op, handler)` wraps an `http.Handler` so each
  request emits a server span and joins any incoming W3C Trace
  Context headers.
- `otel.NewPgxTracer()` returns a `pgx.QueryTracer` you wire onto your
  pgx connection config so every SQL query becomes a span.
- `otel.PublishWithTrace(ctx, js, subject, data)` publishes to NATS
  JetStream while injecting trace context into the message header.
- `otel.ConsumeContext(parent, msg)` extracts that context on the
  consumer side so spans link end-to-end across the bus.

## Why this exists at the foundation level

OpenTelemetry is the architectural keystone called out in
`CLAUDE.md` (A5) and the PRD (D18). Wiring it in from day one means:

- Every metric and span we ever emit goes through a single,
  vendor-neutral pipeline that any acquirer's observability stack
  (Datadog, Splunk, Grafana Cloud, Honeycomb, New Relic, Cisco
  AppDynamics, Cloudflare Workers Logs, etc.) can ingest with a
  config change rather than a code change.
- Cross-domain correlation — the headline reasoning-layer feature in
  Phase 1 — assumes spans actually link across services. Building
  that on top of an OTel foundation is straightforward; bolting it
  onto fragmented instrumentation is the kind of work that takes
  three months and never finishes.
- The same trace ID propagates from the operator's web request →
  control plane → NATS → POP agent → ClickHouse query, which makes
  "why was this canary slow" a single-trace question instead of a
  six-tab tab-and-grep exercise.

## Configuration (environment variables)

All settings have defaults; in dev nothing is required.

| Variable | Default | Meaning |
|---|---|---|
| `NETSITE_OTEL_ENABLED` | `true` | Set to `false` to drop in no-op providers and a no-op shutdown. Useful for unit tests and air-gap deployments without a collector. |
| `NETSITE_OTEL_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC collector address. The dev compose stack lands at `localhost:4317`; production points to your collector. |
| `NETSITE_OTEL_INSECURE` | `true` | Skip TLS on the gRPC connection. Default true matches the dev compose stack. Set to `false` for production cross-cluster setups. |
| `NETSITE_OTEL_SAMPLING_RATIO` | `0.01` | Head-based sampling ratio for traces, parent-based. 0.0–1.0. Set to `1.0` during incidents. |
| `NETSITE_OTEL_SERVICE_NAME` | passed in | resource.service.name attribute. Each binary passes its name (`ns-controlplane`, `ns-pop`, etc.). |
| `NETSITE_OTEL_SERVICE_VERSION` | passed in | resource.service.version attribute. Typically the linker-injected `pkg/version.Version`. |

## Boot pattern

```go
cfg := otel.ConfigFromEnv("ns-controlplane", version.Version)
shutdown, err := otel.Setup(ctx, cfg)
if err != nil {
    log.Fatalf("otel setup: %v", err)
}
defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = shutdown(sctx)
}()
```

## Why head-based sampling at 1%

Phase 0–1 NetSite emits roughly:

- 1 span per canary result × N tests × M POPs × (1/30s) ≈ very large.
- 1 span per BGP UPDATE × thousands of prefixes per minute.
- 1 span per HTTP request to the control plane.

100% sampling at this volume saturates the collector and inflates
storage cost without changing what an operator can answer. 1% head with
parent-based propagation keeps full traces of sampled requests intact
end-to-end, while making the volume manageable. Operators flip to 100%
during incidents via `NETSITE_OTEL_SAMPLING_RATIO=1.0`. This is the
standard dial-in/dial-out approach used by every observability platform
the eight named acquirers operate.

## What we deliberately do not do

- **No vendor-specific exporters.** OTLP only. Datadog/Splunk/etc. each
  have their own collectors that ingest OTLP; talk to those, not their
  proprietary protocols.
- **No log integration in this package.** NetSite uses `slog` directly;
  log-trace correlation is handled by the controlplane logger config,
  not here.
- **No global metric registration helpers.** Service code uses
  `otel.Meter(name)` directly. Wrapping it in a NetSite-specific helper
  would add a layer with no value.
