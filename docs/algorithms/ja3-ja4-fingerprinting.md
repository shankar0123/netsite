# JA3 and JA4 — TLS ClientHello fingerprinting

> Source: [`pkg/canary/tls/ja3.go`](../../pkg/canary/tls/ja3.go),
> [`pkg/canary/tls/ja4.go`](../../pkg/canary/tls/ja4.go).
> Tests: [`pkg/canary/tls/fingerprint_test.go`](../../pkg/canary/tls/fingerprint_test.go).

## Problem statement

Operators investigating "is this client really who they say they are"
need a way to identify TLS clients across connections. The IP address
changes (CGN, mobile, multi-homed clients). The user-agent header is
unreliable (anyone can send any string). The TLS handshake itself,
specifically the **ClientHello**, carries information that is much
harder to forge: the cipher suites the client supports, the
extensions it advertises, the curves and signature algorithms it
offers. JA3 (Salesforce, 2017) and JA4 (FoxIO, 2023) are two
fingerprinting algorithms that distil those fields into a short
identifier.

NetSite's TLS canary captures these fingerprints to detect:

- **MITM proxies in the path.** A canary that consistently produced
  JA3 X but suddenly produces JA3 Y has been intercepted by something
  that re-handshook on its behalf. The new fingerprint reveals the
  proxy's TLS stack.
- **Server-side fingerprint shifts.** The same logic with JA3S/JA4S
  (server-hello equivalents) detects load-balancer changes, TLS-
  termination upgrades, and CDN-fronted swaps.
- **Anomalous client populations** in the flow + RUM data domains
  (Phase 3+) — distinguishing a real Chrome population from a botnet
  that is impersonating Chrome's user-agent but cannot fake Chrome's
  exact extension order.

## Why naive approaches fail

- **User-agent string.** Trivially forgeable; only useful when the
  client is cooperative, which by definition is not the threat model.
- **TLS version + cipher suite alone.** Loses to convergence:
  most modern clients negotiate TLS 1.3 with the same handful of
  cipher suites. Cardinality is too low to distinguish populations.
- **Whole-handshake hash.** Includes per-connection nonces and
  session tickets; every connection produces a unique hash, so the
  fingerprint never matches across visits.

The two algorithms below thread a needle: enough fields to
distinguish populations, deterministic enough to remain stable
across connections from the same client.

## JA3 algorithm

JA3 is the MD5 of a comma-joined string of five fields:

```
SSLVersion,Ciphers,Extensions,EllipticCurves,EllipticCurvePointFormats
```

Each field is the dash-joined decimal representation of the
corresponding ClientHello list. **GREASE values (RFC 8701) are
stripped first** — RFC 8701 reserves 16 specific values that
clients deliberately rotate to keep TLS extensible. Including them
in the fingerprint would make every connection look unique.

### Worked example

A representative TLS 1.2 ClientHello has:

- Version = 0x0303 (decimal 771)
- Cipher suites = TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, ECDHE_RSA_AES_256, AES_128_GCM
  (codes 49195, 49199, 158)
- Extensions = server_name, extended_master_secret, renegotiation_info,
  supported_groups, ec_point_formats, session_ticket, application_layer_protocol_negotiation,
  status_request, signature_algorithms, key_share, padding
  (codes 0, 23, 65281, 10, 11, 35, 16, 5, 13, 28, 21)
- EllipticCurves = x25519, secp256r1, secp384r1 (codes 29, 23, 24)
- ECPointFormats = uncompressed (code 0)

The pre-hash JA3 string is:

```
771,49195-49199-158,0-23-65281-10-11-35-16-5-13-28-21,29-23-24,0
```

The MD5 of that string (32 hex chars, lowercase) is the JA3 hash.

### Why MD5 in 2026

JA3 is a fingerprint, not a cryptographic primitive. The algorithm
predates collision-aware design and has been deployed widely with
MD5; switching to SHA-256 would produce a different identifier and
break interoperability with every existing JA3 catalogue. Our
implementation tags the `crypto/md5` import with a `nolint:gosec`
comment so the linter does not flag it as a vulnerability — gosec
correctly warns about MD5 in cryptographic contexts; this is not one.

### Failure modes

- **Not all clients reorder extensions.** Some legitimate clients
  randomise extension order at handshake time (recent Chrome).
  These produce many different JA3 hashes for what is the same
  client. JA4 fixes this by sorting the extensions before hashing.
- **MD5 collisions on small inputs.** Two distinct JA3 strings can
  produce the same hash. The 128-bit space and the constrained
  string format make accidental collisions astronomically unlikely;
  adversarial collisions are computationally cheap (MD5 is broken)
  but irrelevant — an attacker who can choose their ClientHello
  fields directly does not need to forge a JA3 collision; they just
  copy the target fingerprint.

## JA4 algorithm

JA4 is the algorithm we treat as primary. The string format is:

```
JA4 = <ja4_a> "_" <ja4_b> "_" <ja4_c>
```

- **`ja4_a`** is 10 ASCII characters describing the protocol context:
  - 1 char: `t` for TLS-over-TCP, `q` for QUIC.
  - 2 chars: TLS version (`13` for TLS 1.3, `12` for 1.2, etc.).
  - 1 char: `d` if SNI present, `i` if absent.
  - 2 chars: cipher count (GREASE-stripped, capped at 99).
  - 2 chars: extension count (GREASE-stripped, capped at 99).
  - 2 chars: first-ALPN encoding (first byte + last byte of the
    first ALPN value, restricted to printable 7-bit ASCII; `00` if
    no ALPN).
- **`ja4_b`** is the 12-char SHA-256 prefix of the **sorted**,
  GREASE-stripped, decimal-comma-joined cipher list.
- **`ja4_c`** is the 12-char SHA-256 prefix of `<sorted_extensions>_<signature_algorithms>`,
  where:
  - `sorted_extensions` excludes 0x0000 (SNI) and 0x0010 (ALPN) —
    those signals already live in `ja4_a` — is GREASE-stripped, sorted
    ascending, decimal-comma-joined.
  - `signature_algorithms` is in the **original ClientHello order**
    (NOT sorted), GREASE-stripped, decimal-comma-joined.

### Why JA4 over JA3 going forward

- **Stability under modern Chrome.** Chrome 110+ randomises extension
  order; JA3 hashes shift per connection, JA4 does not because
  `ja4_c` sorts.
- **High-signal context fields.** `ja4_a` carries the SNI-present,
  cipher-count, and ALPN signals as plain text. Operators reading a
  trace can read the JA4 directly without lookup.
- **Two halves with different stability properties.** `ja4_b` (sorted
  ciphers) is what makes JA4 stable; `ja4_c` (sorted extensions plus
  ordered signature algorithms) preserves enough order-sensitive
  information to distinguish clients that ship the same ciphers in
  different SignatureAlgorithm policies.

## Calibration and the false-collision rate

The fingerprint algorithms have no parameters to tune; the only
calibration question is "how often do legitimately different clients
collide on the same fingerprint?" Empirically, the FoxIO project
reports JA4 collisions in the 0.1–1% range across modern browser
populations, with the bulk of collisions being clients that genuinely
share an underlying TLS stack (e.g. multiple Chrome versions on the
same platform). NetSite uses the fingerprint as a *signal*, not a
*decision*: a JA4 mismatch raises confidence that something changed,
but the alert reasoning layer (Phase 1) considers the fingerprint
together with cert chain, IP block, and timing residuals before
firing.

## What we ship in v0.0.5 and what we do not

This commit ships the **algorithms** with vector-tested
implementations. It does not yet wire the fingerprint capture into
the live TLS canary handshake — Go's stdlib `crypto/tls` does not
expose the ClientHello bytes that JA3 and JA4 hash. Closing that
gap requires adopting one of:

- **`uTLS`** (refraction-networking/utls) — a fork of crypto/tls
  that exposes Hello assembly and supports impersonating other
  fingerprints.
- **`dreadl0ck/tlsx`** — a parser library that operates on captured
  bytes (e.g. from a PCAP or a `net.Conn` `Peek`).
- **A custom dialer** that constructs the ClientHello byte-for-byte
  and reads the ServerHello manually.

The dialer adoption is a Phase 1 task. Until then, the TLS canary's
`Result.JA3` and `Result.JA4` fields remain empty strings. The
algorithm code below is exercised by the vector tests and is ready
to receive real `ClientHelloFingerprint` values the moment a
capture path lands.

## Prior art

- Salesforce JA3 reference: <https://github.com/salesforce/ja3>.
- FoxIO JA4 specification: <https://github.com/FoxIO-LLC/ja4>.
- RFC 8701 — Applying GREASE to TLS Extensibility: <https://www.rfc-editor.org/rfc/rfc8701>.
- Anglin et al., "Detecting Sophisticated Phishing with TLS
  Fingerprinting" (2022) — calibration data on JA3/JA4 collision
  rates across enterprise populations.
