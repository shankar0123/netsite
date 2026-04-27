# NetSite Security Posture

> Status: in force from v0.0.12. This document is the authoritative
> reference for the project-wide TLS / encryption / access-control
> posture. Cross-checked at every commit; PR reviews reject changes
> that violate it.

## Why this doc exists

NetSite is intended for acquisition. Acquirers run security diligence
before a term sheet. We want to make that diligence trivial: the
posture is documented, the posture is enforced in code, and the
deployment story states the posture explicitly.

This file captures the trajectory-wide TLS / encryption / access-
control bar across all five phases. If you are adding a new feature
or a new network surface, find the relevant row below and conform.

## Architecture invariant

**A11 (CLAUDE.md): Every operator-facing network surface defaults
to TLS 1.3+. Plaintext is opt-in via explicit env var and emits a
Warn-level log line at boot. Production deployments must not need
the plaintext opt-in.**

Concretely:

- The control-plane HTTP server refuses to start without either a
  TLS cert/key pair OR `NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true`.
- All operator-facing webhooks (alert receivers, integrations) are
  validated as `https://` at construction time. Plaintext is
  available behind a struct-level `AllowInsecure: true` opt-in for
  internal-only use.
- `Strict-Transport-Security` (HSTS) is set on every response when
  the server runs in TLS-listen mode.
- The session cookie is `Secure: true; HttpOnly; SameSite=Lax`.
- Operator documentation (README, runbook) tells operators how to
  use a TLS-terminating reverse proxy (Caddy, nginx, cloud LB) when
  they don't want NetSite handling certs directly.

## Phase-by-phase TLS posture

### Phase 0 — Foundation (current)

| Surface | Posture | Notes |
|---|---|---|
| Control-plane HTTP API | TLS 1.3+ default; plaintext opt-in | `cmd/ns-controlplane/main.go` reads `NETSITE_CONTROLPLANE_TLS_CERT_FILE` / `_KEY_FILE`; refuses to start without TLS or explicit `_ALLOW_PLAINTEXT=true`. |
| Auth session cookie | TLS-only (`Secure=true`) | `pkg/auth/sessions.go`. |
| HSTS | Mounted in TLS-listen mode only | `pkg/api/middleware/hsts.go`. `max-age=31536000; includeSubDomains`. |
| Postgres DSN | Operator-controlled | Recommend `sslmode=verify-full` in production; testcontainers use `sslmode=disable` for hermetic tests. |
| ClickHouse | Native protocol with `secure=true` flag | Recommended for production; dev compose stack uses cleartext over the internal docker network. |
| NATS JetStream | Accepts `tls://` URL | Recommended for production; POP→control-plane traffic is TLS in the recommended deployment. |
| OTel OTLP gRPC | TLS by default; `NETSITE_OTEL_INSECURE=true` for dev | `pkg/integrations/otel/setup.go`. |
| HTTP/TLS canary outbound | Native HTTPS | Operator picks the target URL. |
| SLO webhooks | https-only by default; `AllowInsecure=true` opt-in | `pkg/slo/notifier.go`. The evaluator logs+skips an SLO with a plaintext webhook URL rather than sending the alert payload over HTTP. |
| Prometheus `/metrics` | Inherits the API server's posture | Mounted on the same mux. |

### Phase 1 — BGP + reasoning

| Surface | Posture |
|---|---|
| RIS Live WebSocket | `wss://` only. |
| RouteViews BMP stream | RFC 7854 plaintext over TCP. We treat the BMP feed as a trusted-network assumption; document this as a known-and-accepted limitation in the algorithm doc and the deployment guide. |
| Looking-glass federation | https only; reject `http://` LG endpoints at config time. |
| Cloudflare integration | HTTPS API. |

### Phase 2 — BMP audit + drift + status pages

| Surface | Posture |
|---|---|
| Customer router BMP feed | Plaintext per spec; ns-bgp must be deployed inside the customer's trusted network. Documented in the BMP algorithm doc. |
| **White-label status pages** | **HTTPS-required.** All canonical URLs in feed content (RSS / Atom / share links / OpenGraph) emit `https://` only. The deployment guide bundles a Caddy reference config. |
| IPAM auto-discovery (Netbox / Infoblox) | https only. |

### Phase 3 — Flow + RUM

| Surface | Posture |
|---|---|
| NetFlow / IPFIX / sFlow ingest | UDP plaintext per spec. Trusted-network assumption. |
| **RUM SDK ingest endpoint** | **HTTPS-only.** SDK refuses non-https endpoints at init time (browsers will block beacons from HTTPS sites otherwise — mixed content). Bake into Task 3.x acceptance. |
| Edge plugin templates | Native HTTPS. |
| Datadog / Splunk integrations | HTTPS. |

### Phase 4 — PCAP + air-gap

| Surface | Posture |
|---|---|
| **PCAP upload** | **HTTPS-only.** Inherits the control-plane TLS posture. |
| Air-gap signed bundles | Sigstore / cosign; transport is manual (thumbdrive / cross-domain). N/A for transport TLS. |
| ThousandEyes / Kentik integrations | HTTPS APIs. |

### Phase 5 — Acquisition readiness

| Surface | Posture |
|---|---|
| SSO / SAML / OIDC | HTTPS-required by spec for IdP callbacks. |
| HA / pod-to-pod | mTLS via service mesh in the recommended k8s deployment; Helm chart defaults to a service mesh annotation. |
| Audit log emit | Already covered by OTel TLS. |
| Compliance reporting | TLS is table stakes. |

## Production deployment checklist

Operators putting NetSite into production should verify each of:

- `NETSITE_CONTROLPLANE_TLS_CERT_FILE` and `_KEY_FILE` set, OR a
  TLS-terminating reverse proxy in front with `_ALLOW_PLAINTEXT=true`.
- Postgres DSN has `sslmode=verify-full` (or stronger).
- ClickHouse URL has `secure=true`.
- NATS URL is `tls://...` between ns-controlplane and ns-pop.
- `NETSITE_OTEL_INSECURE=false` (or unset, which defaults to TLS).
- All SLO webhook URLs are `https://` (the notifier rejects others
  by default).
- The deploy host's certificate is rotated before expiry — the
  `cert-manager` manifest in the Helm chart handles this in
  k8s; air-gap deployments must script their own renewal.
- `Strict-Transport-Security` is observed in browser dev tools
  against the live API.

## Keeping this doc current

Every PR that adds or modifies a network boundary updates this file:
the relevant row gets the posture statement, and the algorithm doc
or runbook gets a one-line reference to it. The PROJECT_STATE.md
change log records the security-relevant change.

If you add a network surface that you believe legitimately cannot
use TLS (e.g., a netflow exporter on the export side), document the
trust assumption in the relevant algorithm doc AND add the row to
the table above. "I forgot" is not an acceptable answer in
acquisition diligence.

## Demo and dev paths

The trajectory-wide TLS bar applies to demos and local dev too. We
ship three paths so an operator never has a reason to cut the
corner:

| Path | When to use | How |
|---|---|---|
| `NETSITE_DEV_AUTOTLS=true` | One-off "show this in 5 minutes" demo on the operator's laptop. No cert management, no install. | `make run-controlplane-tls` binds `127.0.0.1:8443` with an in-memory ECDSA-P256 self-signed cert valid for 30 days. The boot log emits the cert SHA-256 fingerprint so `curl --cacert` or browser cert-trust dialogs can pin to it. **Refuses to bind non-loopback addresses** (defence against accidental prod use); the matrix in `cmd/ns-controlplane/devtls.go:isLoopbackAddr` is the gate. |
| `make dev-tls` (mkcert) | Daily local dev, especially when the React shell is running on a different port and needs cookie / SameSite to work cleanly. | One-time `mkcert -install` puts a local CA into the operator's trust store; `make dev-tls` issues `localhost.pem` + `localhost-key.pem` under `deploy/dev-certs/` (gitignored). Every dev session thereafter is browser-clean HTTPS. |
| Caddy sidecar in compose | Compose-stack demos, integration tests, anything pretending to be production but on a laptop. | `deploy/compose/Caddyfile` runs `tls internal` against `localhost:443`. Compose maps :443 → :8443 on the host. The controlplane binds plaintext on 127.0.0.1:8080 (with `NETSITE_CONTROLPLANE_ALLOW_PLAINTEXT=true`); Caddy fronts it with HSTS and an internal-CA cert. Production swap-in is one `tls` directive away. |

### Frontend dev ergonomics

The React shell (Task 0.25) ships a Vite config with `server.https`
reading the same mkcert pair, so a `pnpm dev` session at
`https://localhost:5173` talks to `https://localhost:8443` without
mixed-content / SameSite issues. The session cookie's `Secure=true`
and the (future) cross-origin `SameSite=None` work because both
ends speak TLS.

### RUM SDK localhost carve-out (Phase 3)

The RUM SDK refuses non-`https://` ingest endpoints by default.
The single carve-out is the browser-standard "secure context"
definition: `http://localhost` and `http://127.0.0.1` (and `[::1]`)
are accepted with a Warn emitted to the JS console. Any other
scheme/host requires HTTPS — there is no general-purpose
`insecure: true` flag.

### Public-internet demo (Phase 1+)

A canonical demo URL (TBD: `demo.netsite.dev` or similar) on a
small VM with Caddy + Let's Encrypt. The 90-second demo features
(PCAP replay, swing detection, NL incident query, status pages)
all run there so prospects can see the product live without
spinning up their own deploy. The infrastructure pattern is just
"Caddy + ns-controlplane + ns-pop + Postgres + ClickHouse + NATS";
documented in `docs/demo.md` (lands when there's product worth
showing).

## Out of scope (today)

These are not yet enforced; tracked in PROJECT_STATE.md §16 (Known
Drift) until they are:

- Certificate pinning on outbound webhooks. Considered; rejected for
  v0 because operators routinely rotate their PagerDuty / Slack
  endpoints. Revisit if operators ask for it.
- mTLS for POP→control-plane (Phase 5 add).
- HSTS preload list submission. That's the operator's decision per
  HSTS spec; we provide the header but don't `preload` it.

## References

- RFC 6797 — HTTP Strict-Transport-Security.
- RFC 8446 — TLS 1.3.
- RFC 7854 — BGP Monitoring Protocol (plaintext-over-TCP, by
  design).
- OWASP ASVS V9 (Communication Security) — the bar this doc
  describes.
