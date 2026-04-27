# NetSite developer compose stack

> Files: [`docker-compose.yml`](./docker-compose.yml),
> [`.env.example`](./.env.example),
> [`prometheus.yml`](./prometheus.yml),
> [`otel-collector.yaml`](./otel-collector.yaml),
> [`grafana/provisioning/`](./grafana/provisioning/).
> Healthcheck script: [`../../scripts/wait-healthy.sh`](../../scripts/wait-healthy.sh).

## What this is

A local environment for NetSite development. Six services on the host
network: Postgres, ClickHouse, NATS JetStream, Prometheus, Grafana, and
an OpenTelemetry collector that the NetSite binaries export to.

This is **not** the production deployment. Production is Helm
(Phase 5) or air-gap signed bundles (Phase 4). Compose is for
`make dev`, period.

## Quick start

```sh
# from repo root
cd deploy/compose
cp .env.example .env             # optional — defaults work as-is
docker compose up -d
../../scripts/wait-healthy.sh    # blocks until everything is ready (~60s on a cold start)
```

Endpoints (defaults from `.env.example`):

| Service     | URL                              |
|-------------|----------------------------------|
| Postgres    | `postgres://netsite:netsite@localhost:5432/netsite` |
| ClickHouse (HTTP) | `http://localhost:8123`           |
| ClickHouse (native) | `localhost:9000`                |
| NATS client | `nats://localhost:4222`          |
| NATS HTTP   | `http://localhost:8222`          |
| OTel gRPC   | `localhost:4317`                 |
| OTel HTTP   | `http://localhost:4318`          |
| Prometheus  | `http://localhost:9090`          |
| Grafana     | `http://localhost:3000` (admin / admin) |

## Why this layout

- **Pinned image versions** — every service in the file has a
  specific tag. Floating `:latest` would mean two developers six
  weeks apart get different stacks. The trade-off is that bumping
  Postgres is a deliberate edit + commit, which is what we want.
- **Healthchecks gate dependency order** — Grafana waits for
  Prometheus to be healthy; Otel-collector waits for Prometheus to
  be reachable. The control plane (when wired up) waits on Postgres.
- **Ports overridable via `.env`** — running two compose stacks on
  the same machine for parallel branches is a routine pre-Phase-1
  workflow.
- **Volumes for state** — restarting the stack doesn't wipe the
  database. To wipe deliberately: `docker compose down -v`.
- **Grafana anonymous viewer enabled** — local-only convenience.
  Production never enables this.

## Why these specific services

| Service | Role | Why included now |
|---|---|---|
| Postgres | Relational config, RBAC, SLOs, annotations | Task 0.04+ already use it |
| ClickHouse | Time-series telemetry | Task 0.05+ already use it |
| NATS JetStream | Inter-service event bus | Task 0.06+ already use it |
| OTel collector | Receives spans/metrics from NetSite binaries | Task 0.08 exports to it |
| Prometheus | Scrapes `/metrics` from NetSite binaries and the collector | Task 0.07 mounts the exposer |
| Grafana | Local dashboards | Anchors the Phase 0 demo experience |

Future additions (kept out of compose until they have a service to
run):

- `ns-controlplane` — Task 0.12.
- `ns-pop` — Task 0.17.
- `ns-bgp`, `ns-flow`, `ns-pcap` — Phase 1+.
- An MCP-tool sidecar for the NL incident query — Phase 1.18.

## Bumping image versions

```sh
docker pull <name>:<tag>
docker inspect --format='{{index .RepoDigests 0}}' <name>:<tag>
# replace the `image:` line in docker-compose.yml with the new digest
```

The digest pin format `image: name@sha256:…` is preferred for
production; we use semantic tags here for human readability and pin
via the lockfile of CI's image cache.

## Reset to clean state

```sh
docker compose down -v   # stops services AND removes volumes
docker compose up -d
```

## Common problems

- **Ports already bound.** Edit `.env` to override the port that's
  taken (e.g. `NETSITE_PG_PORT=5433`).
- **Out of memory on `up -d`.** ClickHouse is the biggest tenant;
  Docker Desktop's default 2 GB can be tight. Bump to 4 GB.
- **Healthcheck fails for ClickHouse on first boot.** It needs
  ~10 s to initialise; the healthcheck retries 10× with 5 s gaps,
  which is enough on every machine we've tested. If it persistently
  fails, check `docker logs netsite-clickhouse` for an
  initialisation error (most often: stale data dir from a previous
  ClickHouse version — fix with `docker compose down -v`).
