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

package tls

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// What: the JA4 fingerprint algorithm (FoxIO, 2023). JA4 is the
// successor to JA3 — the design goals were stability across TLS
// extension reordering and the inclusion of ALPN as a structural
// signal, both of which JA3 misses.
//
// How: a JA4 fingerprint is three dot-separated parts:
//
//	JA4 = <ja4_a> "_" <ja4_b> "_" <ja4_c>
//
// where:
//
//	ja4_a — 10 ASCII characters describing the protocol context:
//	         q|t   transport (q=QUIC, t=TLS over TCP)
//	         12|13 TLS version (12 = 1.2, 13 = 1.3, 10 = 1.0, ...)
//	         d|i   SNI present (d) or absent (i)
//	         NN    cipher count, 2 digits
//	         NN    extension count, 2 digits
//	         AA    first ALPN value, 2 chars (lower 7-bit) or "00"
//
//	ja4_b — 12-char SHA-256 prefix of "<sorted_ciphers>" (decimal-
//	         comma-separated, ascending; GREASE stripped). The hash
//	         encoding is lower-case hex; we take the first 12 chars.
//
//	ja4_c — 12-char SHA-256 prefix of
//	         "<sorted_extensions>_<signature_algorithms>" where the
//	         extensions list excludes 0x0000 (SNI) and 0x0010 (ALPN),
//	         is GREASE-stripped, sorted ascending, decimal-comma-joined.
//	         The signature_algorithms list is in original ClientHello
//	         order (NOT sorted); GREASE-stripped; decimal-comma-joined.
//
// Reference spec: https://github.com/FoxIO-LLC/ja4
//
// Why JA4 in addition to JA3: JA3 hashes extensions in order, so a
// browser that randomises extension order produces a different JA3
// per connection. JA4 sorts (except for SNI/ALPN-derived signals
// already in ja4_a), which gives a stable identity across modern
// clients. Operators investigating "is this the same client?"
// questions overwhelmingly want the JA4 answer.
//
// Why ship the algorithm without a live ClientHello capture: same
// argument as JA3 — Go's stdlib does not expose the ClientHello
// bytes. The function below takes a ClientHelloFingerprint and
// computes JA4 deterministically. The dialer-swap commit that wires
// in real captured ClientHellos lands in Phase 1.

// JA4 returns the JA4 fingerprint of f. Returns "" for the zero value
// fingerprint to avoid producing a "constant" hash that operators
// might confuse for a real client signature.
func (f ClientHelloFingerprint) JA4() string {
	if f.Version == 0 && len(f.Ciphers) == 0 && len(f.Extensions) == 0 {
		return ""
	}
	a := f.ja4a()
	b := f.ja4b()
	c := f.ja4c()
	return a + "_" + b + "_" + c
}

// ja4a builds the 10-character "context" part. This is the only JA4
// section that is not a hash — it carries the high-signal facts
// directly so operators can read JA4 strings at a glance.
func (f ClientHelloFingerprint) ja4a() string {
	var b strings.Builder
	b.Grow(10)
	// Transport: t for TLS over TCP (NetSite always today). QUIC
	// support is a Phase 4+ concern alongside HTTP/3.
	b.WriteByte('t')
	// TLS version: 2-digit major.minor encoded as 2 chars. The JA4
	// spec maps 0x0301→"10", 0x0302→"11", 0x0303→"12", 0x0304→"13".
	b.WriteString(versionString(f.Version))
	// SNI: d if present, i if absent.
	if f.SNIPresent {
		b.WriteByte('d')
	} else {
		b.WriteByte('i')
	}
	// Cipher count, 2 digits, GREASE-stripped, capped at 99.
	b.WriteString(twoDigits(len(stripGreaseUint16(f.Ciphers))))
	// Extension count, 2 digits, GREASE-stripped, capped at 99.
	b.WriteString(twoDigits(len(stripGreaseUint16(f.Extensions))))
	// First ALPN value as 2 chars. Per spec: take the first ALPN's
	// first and last bytes; if no ALPN, write "00".
	b.WriteString(firstALPN(f.ALPN))
	return b.String()
}

// ja4b is the sha256-prefix hash of the sorted, GREASE-stripped,
// comma-joined cipher list.
func (f ClientHelloFingerprint) ja4b() string {
	sorted := append([]uint16(nil), stripGreaseUint16(f.Ciphers)...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sha256Prefix12(commaJoinUint16(sorted))
}

// ja4c is the sha256-prefix hash of "<sorted_extensions>_<sig_algs>".
//
// The sorted_extensions list:
//   - excludes 0x0000 (SNI) and 0x0010 (ALPN) per spec;
//   - is GREASE-stripped;
//   - is sorted ascending;
//   - is comma-joined in decimal.
//
// The signature_algorithms list:
//   - is in original ClientHello order (NOT sorted);
//   - is GREASE-stripped;
//   - is comma-joined in decimal.
//
// The two lists are joined with "_" and the SHA-256 of that string is
// truncated to 12 hex chars.
func (f ClientHelloFingerprint) ja4c() string {
	exts := make([]uint16, 0, len(f.Extensions))
	for _, e := range stripGreaseUint16(f.Extensions) {
		if e == 0x0000 || e == 0x0010 {
			continue
		}
		exts = append(exts, e)
	}
	sort.Slice(exts, func(i, j int) bool { return exts[i] < exts[j] })

	sigAlgs := stripGreaseUint16(f.SignatureAlgorithms)
	combined := commaJoinUint16(exts) + "_" + commaJoinUint16(sigAlgs)
	return sha256Prefix12(combined)
}

// commaJoinUint16 produces "a,b,c" from {a,b,c} in decimal.
func commaJoinUint16(in []uint16) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, len(in))
	for i, v := range in {
		parts[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(parts, ",")
}

// sha256Prefix12 returns the first 12 lower-case hex chars of
// SHA-256(s). Used by ja4_b and ja4_c.
func sha256Prefix12(s string) string {
	if s == "" {
		// JA4 uses 12 zeroes when the input is empty so the structure
		// of the hash output is preserved.
		return "000000000000"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// versionString maps a uint16 TLS version to JA4's 2-char encoding.
// Falls back to "00" for values we do not recognise.
func versionString(v uint16) string {
	switch v {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	}
	return "00"
}

// twoDigits returns n as a zero-padded 2-digit string, capped at 99.
func twoDigits(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 99 {
		n = 99
	}
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// firstALPN returns the JA4 first-ALPN encoding: the first byte and
// the last byte of the first ALPN entry, each restricted to printable
// 7-bit ASCII. If there's no ALPN, return "00".
//
// Per spec, non-printable or empty ALPN gives "00".
func firstALPN(alpn []string) string {
	if len(alpn) == 0 || alpn[0] == "" {
		return "00"
	}
	s := alpn[0]
	first := s[0]
	last := s[len(s)-1]
	if !isPrintableASCII(first) || !isPrintableASCII(last) {
		return "00"
	}
	return string([]byte{first, last})
}

func isPrintableASCII(b byte) bool {
	return b >= 0x20 && b <= 0x7E
}
