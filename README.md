# NetSite

Self-hosted network observability — synthetic monitoring, BGP route analysis,
flow analytics, PCAP analytics — with a cross-domain reasoning layer
(causal correlation, natural-language incident query, what-changed,
outage attribution).

**Status:** v0.0.0, pre-alpha. Phase 0 (foundation) in progress.
**License:** Business Source License 1.1, Change Date 2125-01-01,
Change License Apache 2.0. See [`LICENSE`](./LICENSE).

This repository is single-owner. External code contributions to the core
repo are not accepted (catalog repos under
[`shankar0123/netsite-providers`](https://github.com/shankar0123/netsite-providers),
[`shankar0123/netsite-bgp-catalog`](https://github.com/shankar0123/netsite-bgp-catalog),
and `shankar0123/netsite-presets` accept community PRs since their content
is mostly facts).

## Build

Requires Go 1.25+ (driven by `github.com/jackc/pgx/v5`'s minimum). Docker
is required for `make test-integration` (testcontainers spins up a real
Postgres for the migration runner tests).

```sh
make build
./ns version
```

`make test` runs the unit test suite with the race detector. `make
test-integration` runs the integration suite (Docker required). `make
lint` runs `golangci-lint`. `make vet` runs `go vet`.

## Status of components

The CLI (`ns`) currently exposes only `ns version`. Phase 0 work is
sequenced in the project tracker; subsequent commits will land control
plane (`ns-controlplane`), POP agent (`ns-pop`), canary protocols, the
SLO engine, anomaly detection, and the netql DSL.

## Security

Use GitHub's private vulnerability reporting at
<https://github.com/shankar0123/netsite/security/advisories/new>. See
[`SECURITY.md`](./SECURITY.md). Do not file public issues for security
reports.

The trajectory-wide TLS / encryption / access-control posture lives in
[`docs/security.md`](./docs/security.md). NetSite's load-bearing
invariant: every operator-facing network surface defaults to TLS 1.3+
(architecture decision **A11** in
[`CLAUDE.md`](../CLAUDE.md)). Plaintext is opt-in via explicit env var
and emits a Warn-level log line at boot.

### Production deployment checklist

The control plane refuses to start without one of:

- `NETSITE_CONTROLPLANE_TLS_CERT_FILE` and
  `NETSITE_CONTROLPLANE_TLS_KEY_FILE` pointing at PEM-encoded files
  (TLS-listen mode — recommended for direct-to-internet deployments),
  **or**
- `NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true` (when a TLS-terminating
  reverse proxy — Caddy, nginx, cloud LB — sits in front).

Other production-checklist items, all enforced at runtime where
possible:

- Postgres DSN includes `sslmode=verify-full`.
- ClickHouse URL includes `secure=true`.
- NATS URL is `tls://...` between control plane and POP agents.
- `NETSITE_OTEL_INSECURE` unset or `false`.
- All SLO webhook URLs are `https://` (the notifier rejects others
  by default; `AllowInsecure: true` on the struct is the documented
  internal-only escape hatch).
