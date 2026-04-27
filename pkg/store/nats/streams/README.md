# NATS JetStream subject and stream catalog

> Stream and consumer helpers live in [`../streams.go`](../streams.go).
> Connection helpers live in [`../conn.go`](../conn.go).

## Naming convention

Stream names are uppercase with underscores. Subject patterns are
lowercase, dotted, and end with a wildcard so per-tenant or per-event-
type partitioning can be added without renaming the stream.

```
Stream                          Subject pattern
NETSITE_CANARY_RESULTS          netsite.canary.results.>
NETSITE_BGP_UPDATES             netsite.bgp.updates.>
NETSITE_FLOW_RECORDS            netsite.flow.records.>
NETSITE_PCAP_FINGERPRINTS       netsite.pcap.fingerprints.>
NETSITE_ALERTS                  netsite.alerts.>
```

The stream-name → subject-pattern mapping is one-to-one. New domains
land here as separate streams; do not multiplex unrelated event types
into a single stream because per-stream replay and retention are the
unit of recovery.

## Storage choice

Every NetSite stream uses `nats.FileStorage`. JetStream's
`MemoryStorage` loses messages on server restart, which violates the
at-least-once delivery contract NetSite depends on for canary results
and BGP events.

## Retention

Retention is set per stream from the publisher's perspective:

- **Canary results, flow records:** 7 days. Long-term storage lives in
  ClickHouse; NATS is the durable buffer between agents and ingest.
- **BGP updates:** 30 days. Replay of historical BGP-from-flow analysis
  in Phase 3 needs more history.
- **PCAP fingerprints:** 14 days. PCAP analysis is largely synchronous;
  a buffer for cross-component correlation is enough.
- **Alerts:** 7 days. Alert state lives in Postgres; the stream is for
  notifier fan-out.

Set `MaxAge` on the StreamConfig at the publisher's `EnsureStream`
call site. Do not set `MaxBytes` unless you have a hard quota — it
truncates older messages even when they still fit within `MaxAge`.

## Consumer naming

Durable consumers follow `<service>-<purpose>`:

```
canary-controlplane-ingest
bgp-controlplane-ingest
flow-enricher
alerts-notifier-pagerduty
alerts-notifier-slack
```

Two services subscribing to the same subject must use distinct durable
names; otherwise their delivery sets overlap and at-least-once becomes
"each side gets a random subset."

## Idempotency

Both `EnsureStream` and `EnsureConsumer` are convergence operations:
they create if absent, update if drifted, no-op if matching. Calling
them on every process start is the intended pattern. Operators never
need to hand-create JetStream resources for NetSite to boot
successfully.

## What we deliberately do not do

- **No subject-mapping (request/reply with response subjects).** The
  request/reply pattern complicates retention and routing semantics.
  Where NetSite needs RPC, it uses gRPC against the control plane,
  not NATS.
- **No KV or Object Store buckets.** Configuration data lives in
  Postgres; binary blobs (PCAPs) live on filesystem/S3. JetStream KV
  is a tempting but lossy substitute for either.
- **No cluster/leaf-node config in v1.** Single-node JetStream is
  sufficient for v1 scale. Replicated streams arrive in Phase 5
  alongside multi-region HA work.
