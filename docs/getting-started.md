# Getting started with NetSite

> The long-form companion to the README. The README is the elevator
> pitch + 60-second quickstart; this doc walks through every
> Phase 0 surface — auth, canaries, SLOs, anomaly detection, netql,
> workspaces, annotations — with the what/how/why structure
> (CLAUDE.md Documentation Discipline §). Read top-to-bottom for
> the operator's first day; jump to a section when you're tracking
> down a specific feature.
>
> **Audience.** Operators bringing up NetSite for the first time.
> Assumes you know what canaries / SLOs / TLS / Postgres are at the
> "I've used Datadog or Grafana" level; defines anything beyond
> that the first time it shows up.
>
> **Phase coverage.** Everything below is Phase 0 — what's actually
> shipped and CI-green as of v0.0.14. Phase 1+ surfaces (BGP, flow,
> PCAP, NL incident query, status pages) are noted at the end of
> each section as "what's coming".

---

## What is NetSite, in one paragraph

NetSite is a self-hosted observability platform for the
network-layer stuff that pure application monitoring tools miss:
synthetic checks across geographic POPs (POP = "point of presence",
a place you run an agent from), BGP route analysis, flow telemetry,
PCAP analysis, and a cross-domain reasoning layer that joins them.
Phase 0 ships synthetic monitoring (DNS, HTTP, TLS canaries),
multi-window multi-burn-rate SLO alerting, seasonal-aware anomaly
detection, an English-shaped query language called netql, saved-
view bundles called workspaces, and pinned operator notes called
annotations. Everything is one Go binary plus four backing stores
(Postgres, ClickHouse, NATS JetStream, Prometheus) packaged in a
Docker Compose dev stack. Phase 1+ adds the BGP / flow / PCAP /
status-page / reasoning-layer pieces — see the PRD if you have
access; the public outline is in the README.

---

## Why this exists

Three problems we've watched expensive SaaS observability stacks
fail to solve cleanly:

1. **The ones that own the data own the leverage.** Datadog,
   Splunk, New Relic — your data is in their cloud. Long-tenure
   teams accumulate enough lock-in that switching costs more than
   the product is worth, and the per-seat / per-host pricing
   compounds. NetSite is a single binary you run on your own
   infrastructure. No ingest charge, no per-seat charge, no
   "send everything to our cloud and we'll figure out compliance
   later" anti-pattern. The license (BSL 1.1) prevents a competitor
   from running it as a hosted service against you, but it's
   source-available and the change date (2125) makes the
   "what if you go away" worry moot.

2. **Most observability stacks are application-first; the network
   below is opaque.** When the alert fires at 03:14, you can see
   the application's error rate spike but not the BGP swing that
   caused it, the upstream DNS resolver that started returning
   `SERVFAIL`, the JA3 fingerprint change that broke a downstream
   client. NetSite is built to put those signals next to the
   application signals you already have. Phase 0 is the synthetic
   monitoring foundation; Phases 1–4 add BGP, flow, PCAP, and the
   reasoning layer that joins them.

3. **Operations docs are usually written for the people who built
   the system, not the people who have to run it.** This doc, the
   README, the algorithm docs, and the security posture doc are
   the proof we take that seriously. If you find a gap, that's a
   bug — file it.

---

## How the pieces fit together

Two binaries do the work: `ns-controlplane` (one per cluster) and
`ns-pop` (one per geographic POP — you decide how many, where).
`ns-controlplane` owns the REST API, the auth flow, the SLO
evaluator goroutine, the NATS-to-ClickHouse ingest consumer, and
the embedded React dashboard. `ns-pop` runs canary checks on a
schedule and publishes results to NATS.

```
   ┌─────────┐                                ┌────────────────────┐
   │ ns-pop  │ ── netsite.canary.results.> ──▶│   ns-controlplane  │
   │  (1+)   │   NATS JetStream subject       │     (1, HTTPS)     │
   └─────────┘                                │                    │
                                              │  /v1/* REST + auth │
   POPs publish:                              │  SLO evaluator     │
   {test_id, pop_id,                          │  embedded React UI │
    observed_at, latency_ms,                  │                    │
    error_kind, ja3, ja4, ...}                └─┬─────────▲────────┘
                                                │         │
                                              inserts    queries
                                                ▼         │
                                       ┌──────────────────┴┐
                                       │   ClickHouse 24   │
                                       │   canary_results  │
                                       └───────────────────┘

                                       ┌───────────────────┐
                                       │   Postgres 16     │  tenants, users, sessions,
                                       │   (relational)    │  tests catalog, SLO catalog,
                                       │                   │  workspaces, annotations
                                       └───────────────────┘
```

A third binary, `ns`, is the operator CLI. Today it's small —
`ns version` and `ns seed admin`. Tomorrow it'll grow into the
"netctl" pattern (apply YAML configs from disk, etc.).

The four backing stores split responsibilities:

- **Postgres 16** — relational, transactional state. Tenants,
  users, sessions, the canary tests catalog, the SLO catalog,
  workspaces, annotations.
- **ClickHouse 24** — high-cardinality time-series. Canary
  results today; flow records, BGP updates, PCAP-derived rows in
  later phases. We picked ClickHouse over Prometheus for these
  because flow data alone would explode any TSDB built around the
  metrics-not-logs assumption.
- **NATS JetStream** — durable event bus between POPs and the
  control plane. Single-binary, simpler ops than Kafka, adequate
  for v1 scale.
- **Prometheus** — scrape target for the control plane's own
  metrics (request rates, evaluator runs, ingest throughput).
  Industry-standard for service-level metrics.

Why this split rather than one big store: each one is best in
class at its job, and the operational surface of running them is
small enough that bundling Postgres + ClickHouse + NATS in a
single Compose stack is fine for any sub-petabyte deployment.

---

## Quickstart 1 — Empty dashboard in 60 seconds

Read this first if you've never run NetSite before. Goal: see the
React dashboard render against the real backend, locally, over
HTTPS, with no real data yet.

**Prereqs**: Go 1.25+, Docker, pnpm + Node ≥ 20.

```sh
git clone https://github.com/shankar0123/netsite
cd netsite

cd deploy/compose && docker compose up -d && cd ../..
./scripts/wait-healthy.sh

make build
NETSITE_SEED_PASSWORD='somethinglongand_secure' \
  ./ns seed admin --email you@example.com --tenant-id tnt-default

make build-all
make run-controlplane-tls
```

That last command binds `https://127.0.0.1:8443` and prints a SHA-
256 fingerprint for the ephemeral self-signed cert. Open the URL,
accept the browser warning, log in. You'll see three cards
(backend health, canary tests, SLOs) — empty for now.

If the browser warning bothers you, run `make dev-tls` once. That
uses [mkcert](https://github.com/FiloSottile/mkcert) to install a
local CA into your system trust store; subsequent dev sessions are
browser-clean HTTPS.

**Why the HTTPS-by-default**: see CLAUDE.md A11 and `docs/security.md`.
Short version: production deploys default to TLS, dev should match
so we don't ship a "we'll add TLS later" trap.

---

## Quickstart 2 — Real data flowing in 10 minutes

Picking up from quickstart 1 with the controlplane on `:8443`.

```sh
COOKIE=/tmp/ns-cookies.txt

# Sign in.
curl -k -X POST https://localhost:8443/v1/auth/login \
  -H 'Content-Type: application/json' -c $COOKIE \
  -d '{"tenant_id":"tnt-default","email":"you@example.com",
       "password":"somethinglongand_secure"}'

# Register a POP.
curl -k -X POST https://localhost:8443/v1/pops -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{"id":"pop-dev-local","name":"Dev Local","region":"local"}'

# Configure an HTTP canary against example.com every 30s.
curl -k -X POST https://localhost:8443/v1/tests -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{
    "kind":"http",
    "target":"https://example.com/",
    "interval_ms": 30000,
    "timeout_ms": 5000,
    "config": {"method":"GET", "expected_status": 200}
  }'
# → returns the test_id; note it. Call it $TID.

# Point ns-pop at the controlplane.
cat > /tmp/pop.yaml <<EOF
pop_id: pop-dev-local
nats_url: nats://localhost:4222
tests:
  - id: $TID
    tenant_id: tnt-default
    kind: http
    target: https://example.com/
    interval_ms: 30000
    timeout_ms: 5000
EOF
NETSITE_POP_CONFIG=/tmp/pop.yaml ./ns-pop &

# Wait ~30s, query results.
curl -k -b $COOKIE "https://localhost:8443/v1/tests/$TID/results?limit=5"
```

You should see five rows of results — `latency_ms`, `error_kind`,
the per-phase timing breakdown, `pop_id`. Refresh the dashboard;
the canary tests card now shows count = 1.

**What just happened**: `ns-pop` ran the HTTP probe every 30s,
serialised each result to JSON, published it on NATS subject
`netsite.canary.results.<test-id>`. The controlplane's ingest
consumer pulled each message, inserted into ClickHouse
`canary_results`. The REST query at `/v1/tests/{id}/results`
selected from that table with tenant scoping at the SQL level.

---

## Quickstart 3 — Production deploy

The full env-var matrix is in the README's
[Configuration reference](../README.md#configuration-reference).
The production checklist is in `docs/security.md`. The summary:

1. Either supply `NETSITE_CONTROLPLANE_TLS_{CERT,KEY}_FILE` for
   direct TLS-listen mode, or set
   `NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true` and put a TLS
   terminator (Caddy / nginx / cloud LB) in front. The control
   plane refuses to start without one of these.
2. Postgres DSN includes `sslmode=verify-full`.
3. ClickHouse URL includes `secure=true`.
4. NATS URL is `tls://...`.
5. `NETSITE_OTEL_INSECURE` is unset or `false`.
6. Every SLO `notifier_url` is `https://`.

`deploy/compose/Caddyfile` is a working reference for the
"controlplane behind Caddy" pattern. Production swap-in is one
directive — replace `tls internal` with cert paths or
`tls admin@yourdomain.com` for ACME.

---

## Auth: tenants, users, sessions

**What.** A *tenant* is the top-level isolation boundary; every
row in every table has a `tenant_id` column and every query is
scoped at the SQL level. A *user* belongs to one tenant and has
one of three *roles*: `viewer`, `operator`, `admin`. A *session*
is an opaque 16-byte cookie that ties an HTTP request to a user.

**How.** `POST /v1/auth/login` takes `{tenant_id, email, password}`,
verifies the password against the bcrypt hash (cost 12 by default;
configurable via `NETSITE_AUTH_BCRYPT_COST`, floor 10), and sets a
12-hour `Secure; HttpOnly; SameSite=Lax` cookie named
`ns_session`. The cookie value is opaque (the actual session id
lives server-side in Postgres); rotation is just deleting the row
and re-issuing.

`POST /v1/auth/logout` is idempotent — clears the cookie and
deletes the session row.

`GET /v1/auth/whoami` returns the resolved user; an anonymous
request gets 401.

**Why opaque cookies, not JWTs.** JWTs put the authority in the
token. Once issued, a JWT is valid until expiry (or until you
build a JWT blocklist, which negates the stateless argument).
Server-side opaque sessions let us revoke instantly — `DELETE FROM
sessions WHERE user_id = $1` and the user is logged out
everywhere.

**Why bcrypt cost 12 (not Argon2 / scrypt).** bcrypt at cost 12 is
roughly 250 ms per attempt on a 2024-class CPU — well above the
threshold where online brute force matters. Argon2 is stronger
on memory-hard side channels, but its tuning surface is wider
and the deployment story (matching server memory + parallelism to
attack model) is operator-hostile. If a customer asks for Argon2
post-acquisition, switching is a localised change in `pkg/auth`.

### Seed your first admin

```sh
NETSITE_SEED_PASSWORD='your-real-password-here' \
  ./ns seed admin --email you@example.com --tenant-id tnt-default
```

The CLI talks directly to Postgres (using
`NETSITE_CONTROLPLANE_DB_URL`); no controlplane needed. The
password reads from the env var (preferred over the `--password`
flag, which would land in shell history). Reads `Min Password
Length = 12` — anything shorter is rejected.

### Add another user

Today: hit the auth Service from a Go program. The full RBAC
admin endpoint (`POST /v1/users`) lands in v0.0.16+ alongside the
React shell's admin route. Phase 0 ships everything an operator
needs to log in and exercise the API; multi-user admin tooling
arrives once the React shell can render it.

### What's coming

SSO / SAML / OIDC is Phase 5 (acquisition readiness). RBAC at
finer than `<resource>:<verb>` granularity (per-tenant overrides,
per-resource ACLs) is Phase 5.

---

## Canaries: synthetic monitoring

**What.** A *canary* is a periodic check from a POP against a
target. Three protocols ship today: DNS (resolve a name, check
the answer), HTTP (GET/POST a URL, check status + body), TLS
(handshake to a host:port, capture the cert chain + JA3/JA4
fingerprints).

**How.** `POST /v1/tests` registers a test in the catalog. The
POP agent reads its YAML config (the `tests` list), starts one
goroutine per test, jitters within `[0, interval)` to avoid the
thundering herd, and ticks every `interval_ms`. Each result —
timing breakdown, error kind, fingerprint — is published as JSON
on NATS `netsite.canary.results.<test-id>`. The controlplane
consumer pulls each message and inserts into ClickHouse
`canary_results`.

The canary table is partitioned by `toYYYYMM(observed_at)`,
ordered by `(tenant_id, test_id, observed_at)`, with a 90-day
TTL. Order key choice means "tenant X, test Y, last 24h" — the
canonical query — hits a tight range scan.

**Why three protocols, not one.** DNS / HTTP / TLS check
distinct failure modes. A DNS answer can change without HTTP
breaking (CDN failover); an HTTP 200 can hide a TLS cert
expiring next week (some clients don't validate); a TLS handshake
can succeed against a cert chain that's about to be revoked.
Running all three against your critical endpoints catches the
union of failures.

**Why ICMP isn't here yet.** Unprivileged ICMP sockets are
platform-specific (Linux + macOS + Windows all differ); shipping
a portable implementation is more work than the value of "is the
host pingable" warrants for v0. ICMP arrives in Phase 1 alongside
the BGP work.

### Define a test from the command line

```sh
curl -k -X POST https://localhost:8443/v1/tests -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": "http",
    "target": "https://api.yourcompany.com/health",
    "interval_ms": 30000,
    "timeout_ms": 5000,
    "config": {
      "method": "GET",
      "expected_status": 200,
      "headers": {"Accept": "application/json"}
    }
  }'
```

The `config` blob is JSONB in Postgres; protocol-specific knobs
go in there (DNS record type, TLS `InsecureSkipVerify`, HTTP
expected body regex when that lands).

### Add another POP

A POP is just a place you run `ns-pop`. Register it in the
catalog so the dashboard knows it exists:

```sh
curl -k -X POST https://localhost:8443/v1/pops -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{"id":"pop-fra-01","name":"Frankfurt 1","region":"eu-central"}'
```

Then run `ns-pop` on a host in Frankfurt with a YAML that names
`pop_id: pop-fra-01`. Same NATS URL as the controlplane. That's
it — the agent registers itself by publishing results; nothing
else needs to know it exists.

### Mesh canaries

The POP agent supports a "mesh" mode: every POP probes every
other POP's health URL. Useful for diagnosing inter-POP latency
asymmetries. Configure via the YAML's `mesh.peers` field; the
agent generates one HTTP test per peer with deterministic ids
(`tst-mesh-<self>-<peer>`) so the rows are correlatable.

### What's coming

ICMP (Phase 1). UDP / QUIC probes (Phase 1+). Synthetic browser
runs via headless Chrome (Phase 3, alongside the RUM SDK).
Live ClientHello capture for JA3/JA4 (deferred to Phase 1; today
the fingerprint is computed from the negotiated state).

---

## SLOs: error-budget alerting

**What.** An *SLO* (service-level objective) says "the X
indicator over the last Y window should be ≥ Z%". Example:
"canary success rate over 30 days should be ≥ 99.9%".

**How (the math).** For each enabled SLO the evaluator goroutine
runs every 30 seconds:

1. Compute the SLI (service-level indicator) over four canonical
   windows: 5 min, 30 min, 1 h, 6 h.
2. For each (short, long) window pair, compute the burn rate —
   how fast the error budget is being spent relative to the rate
   that would exhaust 30 days of budget in 30 days.
3. If both the short and long burn exceed the threshold, fire
   the alert (POST the JSON BurnEvent to the SLO's
   `notifier_url`).

The four-window pair table:

| Pair | Threshold | Severity | What it catches |
|---|---|---|---|
| 5 min ∧ 1 h | 14.4 | fast burn (page) | Outage in progress |
| 30 min ∧ 6 h | 6.0  | slow burn (ticket) | Slow degradation |

**Why both windows have to fire.** A single window with threshold
14.4 fires immediately on any short-lived bad minute (false
positives during glitches). Long-only with threshold 1 takes
hours to react. The intersection of short + long catches
"sustained badness" without catching "one bad minute" or
"deferred budget exhaustion".

The full math is in
[`docs/algorithms/multi-window-burn-rate.md`](./algorithms/multi-window-burn-rate.md).
Read it before defining your first SLO; the choice of objective
percentage vs window vs threshold is the most operator-hostile
part of SLO theory.

### Define an SLO

```sh
curl -k -X POST https://localhost:8443/v1/slos -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "API success rate",
    "sli_kind": "canary_success",
    "sli_filter": {"test_id": "tst-abc12345"},
    "objective_pct": 0.999,
    "window_seconds": 2592000,
    "notifier_url": "https://hooks.your-pagerduty.io/ns",
    "enabled": true
  }'
```

`window_seconds: 2592000` is 30 days. `notifier_url` MUST be
`https://`; the controlplane rejects `http://` URLs at construction
time. (The struct-level `AllowInsecure: true` opt-in exists for
internal-only receivers without TLS; it's deliberately not exposed
on the REST surface.)

### Read the SLO state

```sh
curl -k -b $COOKIE https://localhost:8443/v1/slos
```

Each SLO carries:

- `last_status`: `unknown | no_data | ok | slow_burn | fast_burn`.
  `no_data` means the SLI source (ClickHouse for canary_success)
  has zero rows in the window — usually a misconfiguration.
- `last_burn_rate`: numeric, comparable to the threshold table.
- `last_alerted_at`: the most recent fire. Cooldown is 1 h by
  default; the same SLO can't re-page faster than that.

When the status flips to `fast_burn` or `slow_burn`, the webhook
gets a JSON BurnEvent with the SLO id, status, burn rate, and
the windows. Recovery webhooks (back to `ok`) are deferred to
Phase 1 — today's loop is fire-on-burn, log-on-recovery.

### What's coming

Recovery webhooks (Phase 1). Per-tenant webhook routing (Phase 5).
Slack / PagerDuty / OpsGenie native integrations rather than
generic webhooks (Phase 5). Tickets-vs-pages third severity
(Phase 1).

---

## Anomaly detection: seasonal-aware

**What.** Given a time-series (canary success rate, latency
percentile, anything that varies over time), detect "this point
is unusual" without false-positiving on every Saturday morning.

**How.** Two methods, chosen by data density:

- **Holt-Winters additive triple-exp smoothing** (when 2 cycles ≤ n
  < 4 cycles). Tracks level + trend + seasonal components,
  produces a one-step forecast, residual = actual − forecast.
- **Simplified seasonal decomposition** (when n ≥ 4 cycles). A
  simplification of full STL-LOESS that uses centred moving
  averages instead of LOESS smoothers — 40 lines of code for 80%
  of the operational value; full STL arrives in Phase 1 once
  real-world calibration data is available.

Severity is the residual divided by the median absolute deviation
(MAD) of the in-sample residuals: `none < 3 ≤ watch < 5 ≤ anomaly
< 8 ≤ critical`. MAD is robust to outliers — a real outage in
the recent fit window doesn't blow up the residual scale and
silence subsequent alerts.

**Why MAD, not stddev.** stddev gets inflated by exactly the
points we're trying to detect. MAD ignores them by construction.
1.4826 × MAD ≈ σ for normal data, and unlike σ, MAD tolerates
~50% bad data without breaking down.

**Why two methods rather than one.** SLI series differ. Some are
dominated by drift (level changes); HW handles drift via the
trend term. Others are dominated by stable seasonality; STL's
per-phase mean is more interpretable. The chooser picks one per
call so the Verdict carries a single explainable answer.

The full algorithm doc, including calibration methodology and
failure modes, is at
[`docs/algorithms/anomaly-detection.md`](./algorithms/anomaly-detection.md).

### Use it programmatically

The HTTP surface for anomaly detection lands in v0.0.15+ alongside
the React shell. Today, programmatic use:

```go
import "github.com/shankar0123/netsite/pkg/anomaly"

series := anomaly.Series{ /* time-stamped points */ }
v, err := anomaly.Detect(series, anomaly.Config{Period: 24})
fmt.Println(v.Method, v.Severity, v.Reason)
```

Calendar suppression (silence alerts during scheduled
maintenance) is supported via `Config.Calendar`. Suppressed
points still compute the residual + severity (so dashboards stay
accurate); they just never cross the `SeverityAnomaly` threshold.

### What's coming

Full STL-LOESS (Phase 1). Recurrence rules (weekly / monthly) for
calendar suppression (Phase 1). Multiplicative Holt-Winters for
series whose seasonality scales with level (Phase 1). Adaptive
period detection so operators don't have to specify (Phase 1).

---

## netql: the query language

**What.** A small English-shaped DSL: `latency_p95 by pop where
target = 'api.example.com' over 24h`. Compiles to ClickHouse SQL
or PromQL depending on where the metric lives. The full grammar
is locked in
[`docs/algorithms/netql-language.md`](./algorithms/netql-language.md).

**How.** Hand-rolled lexer (one function per token kind), hand-
rolled recursive-descent parser (one function per non-terminal),
type-checker against a metric registry, two backend translators
(ClickHouse, PromQL). The metric registry knows three things per
metric: which backend it lives in, which columns / labels it can
be grouped by, which can be filtered by + their value type.

**Why a DSL rather than raw SQL/PromQL.** Two reasons:

1. The good queries are short. *"latency_p95 by pop over 24h"*
   compresses to seven words; the equivalent ClickHouse SQL is
   80+ characters. Operators don't write the long form
   repeatedly; they copy-paste a stale version.
2. The data lives in two backends. Asking the same question in
   ClickHouse SQL and PromQL produces two different expressions
   that drift over time. One DSL, two compilers, never drift.

We compile to inspectable SQL/PromQL so netql is the on-ramp,
not the ceiling — operators can read what runs and graduate to
the underlying language for advanced cases.

### Use it from Go today

```go
import "github.com/shankar0123/netsite/pkg/netql"

q, _ := netql.Parse(
  "latency_p95 by pop where target = 'api.example.com' over 24h")
out, _ := netql.TranslateClickHouse(q, netql.DefaultRegistry(), "tnt-default")
fmt.Println(out.SQL, out.Args)
```

The `tenant_id = $1` predicate is **always** injected by the
translator. It is impossible to write a netql query that escapes
the caller's tenant.

### Available metrics

| Metric | Backend | What |
|---|---|---|
| `success_rate` | ClickHouse | `countIf(error_kind = '') / count(*)` over canary_results |
| `latency_p95` | ClickHouse | `quantile(0.95)(latency_ms)` over canary_results |
| `count` | ClickHouse | `count(*)` over canary_results |
| `request_rate` | Prometheus | `sum [by ...](rate(netsite_http_requests_total{...}[5m]))` |
| `request_latency_p95` | Prometheus | `histogram_quantile(0.95, sum by (le, ...) (rate(netsite_http_request_duration_seconds_bucket[5m])))` |

More metrics arrive as new backends ship — `bgp_updates_per_sec`
in Phase 1, `flow_bytes` in Phase 3, `pcap_count` in Phase 4.

### What's coming

`/v1/netql` REST endpoint (v0.0.15) returns the parsed AST and
the translated SQL/PromQL — the "show me the SQL" reveal that
makes the DSL a teaching tool. Monaco editor + autocomplete in
the React shell (v0.0.15). String-value completion (suggest valid
`route` values etc.) — Phase 1.

---

## Workspaces: saved-view bundles

**What.** A *workspace* is a list of pinned views (each view =
name + URL/deep-link + optional note) with a name on top. Useful
when you want to revisit the same set of charts every Monday.

**How.** `POST /v1/workspaces` with a name and a list of views.
`PATCH /v1/workspaces/{id}` to update. `POST /v1/workspaces/{id}/share`
mints a short slug with a 7-day default expiry; the public
endpoint `/v1/share/{slug}` resolves the slug and returns a
sanitised version of the workspace (tenant + owner stripped so
the slug doesn't leak ownership).

**Why share via a slug rather than a JWT or per-recipient ACL.**
v0 share semantics are "send this URL to a colleague who's
already in your tenant, they look at it for a week". A slug + an
expiry covers that. Multi-recipient + per-recipient revocation +
signed-with-tenant-CA arrive in Phase 2 alongside the white-label
status pages, which need the same primitive.

### Make a workspace

```sh
curl -k -X POST https://localhost:8443/v1/workspaces -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Q3 incident review",
    "description": "Pinned views for the Q3 retrospective.",
    "views": [
      {"name": "API p95 by POP", "url": "/dashboard/canary?metric=latency_p95"},
      {"name": "Errors by error_kind", "url": "/dashboard/canary?metric=count&groupby=error_kind"}
    ]
  }'
```

### Share it

```sh
curl -k -X POST https://localhost:8443/v1/workspaces/wks-abc12345/share -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{"ttl_seconds": 604800}'   # 7 days; omit for the default
```

The response includes `share_slug`. Hand that slug (or the URL
`https://your-host/v1/share/<slug>`) to anyone who needs read-
only access for the next week. Revoke with `DELETE /v1/workspaces/{id}/share`.

---

## Annotations: pinned operator notes

**What.** A short markdown note pinned to a (scope, scope_id,
timestamp) tuple. Examples: "rolled forward 12:30 UTC" pinned to
a canary failure; "maintenance window started" pinned to a POP;
"deploy bumped reverse-proxy version" pinned to a test.

**How.** `POST /v1/annotations` with scope (`canary | pop |
test`), scope_id (`tst-foo`), timestamp (RFC3339), and body_md.
LIST with optional filters: `?scope=canary&scope_id=tst-foo&from=...&to=...&limit=...`.
The dashboard renders annotations as markers on whichever
timeline matches their scope.

**Why immutable.** An annotation's role is to record what an
operator noted at a moment in time. Mutating the body would
invalidate the audit trail. There is no PATCH endpoint;
correcting a typo means delete + recreate, and the deletion is
itself a fact in the timeline.

### Pin one

```sh
curl -k -X POST https://localhost:8443/v1/annotations -b $COOKIE \
  -H 'Content-Type: application/json' \
  -d '{
    "scope": "canary",
    "scope_id": "tst-abc12345",
    "at": "2026-04-27T12:30:15Z",
    "body_md": "Rolled forward at 12:30 UTC. PR https://github.com/.../1234."
  }'
```

### Read the timeline

```sh
curl -k -b $COOKIE \
  "https://localhost:8443/v1/annotations?scope=canary&scope_id=tst-abc12345&limit=20"
```

---

## Where to go next

| When | Doc |
|---|---|
| Production deploy planning | [`docs/security.md`](./security.md) |
| Defining your first SLO | [`docs/algorithms/multi-window-burn-rate.md`](./algorithms/multi-window-burn-rate.md) |
| Tuning anomaly thresholds | [`docs/algorithms/anomaly-detection.md`](./algorithms/anomaly-detection.md) |
| Writing netql | [`docs/algorithms/netql-language.md`](./algorithms/netql-language.md) |
| Understanding JA3/JA4 | [`docs/algorithms/ja3-ja4-fingerprinting.md`](./algorithms/ja3-ja4-fingerprinting.md) |
| Architecture in detail | [`docs/architecture.md`](./architecture.md) |
| The full REST API | [`api/openapi.yaml`](../api/openapi.yaml) |

If a question isn't answered in any of the above, that's a doc
bug — file it.
