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
	"crypto/md5" //nolint:gosec // JA3 spec mandates MD5; this is not a cryptographic use
	"encoding/hex"
	"strconv"
	"strings"
)

// What: the JA3 fingerprint algorithm (Salesforce, 2017). JA3 hashes
// a deterministic string of the TLS ClientHello fields a peer sends,
// producing an MD5-encoded identifier of the *client*. Servers see
// the same JA3 from a given client across connections, which makes
// it useful for fingerprinting bots, malware, and unexpected
// configurations.
//
// How: the canonical JA3 string is the comma-separated decimal
// concatenation of five field-lists:
//
//	SSLVersion,Ciphers,Extensions,EllipticCurves,EllipticCurvePointFormats
//
// where each list's values are dash-joined. GREASE values (RFC 8701)
// are stripped before serialisation. The MD5 of the lower-case hex
// of that string is the JA3 hash. Reference:
// https://github.com/salesforce/ja3
//
// Why MD5 in 2026 (despite collisions on small inputs being trivial):
// JA3 is a fingerprint, not a cryptographic primitive. The algorithm
// is fixed in the spec; using SHA-256 would produce a different,
// incompatible identifier. We mark md5 with a //nolint:gosec
// because gosec rightly flags md5 imports in case anyone forgot the
// fingerprint context.
//
// What we deliberately do NOT do here: capture the actual
// ClientHello from a live TLS handshake. Go's stdlib `crypto/tls`
// does not expose the ClientHello bytes, and we are not yet adopting
// a custom dialer (uTLS, refraction-networking/utls, dreadl0ck/tlsx)
// for v0. The functions below take a ClientHelloFingerprint struct
// the caller assembles from whatever source is available. The TLS
// canary Runner leaves the JA3/JA4 fields empty until a future
// dialer-swap commit exposes the bytes. See
// docs/algorithms/ja3-ja4-fingerprinting.md.

// ClientHelloFingerprint holds the inputs to JA3 / JA4. It is a
// minimal record of the TLS ClientHello fields needed by both
// algorithms. Construct it from any source — a custom TLS dialer,
// a captured PCAP, a vendor-supplied diagnostic — and pass it to
// JA3 / JA4 to compute the hash.
type ClientHelloFingerprint struct {
	// Version is the TLS version offered by the client, in raw
	// uint16 form (e.g. 0x0303 = TLS 1.2 = decimal 771). JA3 takes
	// the decimal directly.
	Version uint16

	// Ciphers is the list of cipher-suite codes from the ClientHello.
	Ciphers []uint16

	// Extensions is the list of extension type codes, in the order
	// they appear in the ClientHello. JA3 strips GREASE; JA4 sorts
	// (except for the SNI/ALPN extensions per the JA4 spec).
	Extensions []uint16

	// EllipticCurves is the list of supported_groups codes (named
	// curves) the client offered. JA3 includes them; JA4 does not.
	EllipticCurves []uint16

	// EllipticCurvePointFormats is the list of ec_point_formats
	// codes. JA3 includes them; JA4 does not.
	EllipticCurvePointFormats []uint8

	// SignatureAlgorithms is used by JA4 (not JA3).
	SignatureAlgorithms []uint16

	// ALPN is the list of ALPN protocol identifiers offered.
	// JA4 uses the FIRST ALPN value as a single character.
	ALPN []string

	// SNIPresent reports whether the ClientHello contained an
	// SNI extension. JA4 uses this as a single 'd' or 'i' flag.
	SNIPresent bool
}

// JA3 returns the JA3 fingerprint of f as a 32-character lower-case
// hex MD5. Returns the empty string if f is the zero value, since
// hashing an empty fingerprint produces a misleading "constant" hash
// that operators would mistake for a real fingerprint.
func (f ClientHelloFingerprint) JA3() string {
	if f.Version == 0 && len(f.Ciphers) == 0 && len(f.Extensions) == 0 {
		return ""
	}
	return ja3Sum(f.JA3String())
}

// JA3String returns the canonical pre-hash JA3 string. Operators use
// this for debugging — a JA3 hash that doesn't match an expected
// value is much easier to diagnose when the underlying string is
// also visible.
func (f ClientHelloFingerprint) JA3String() string {
	parts := []string{
		strconv.FormatUint(uint64(f.Version), 10),
		joinUint16(stripGreaseUint16(f.Ciphers)),
		joinUint16(stripGreaseUint16(f.Extensions)),
		joinUint16(stripGreaseUint16(f.EllipticCurves)),
		joinUint8(f.EllipticCurvePointFormats),
	}
	return strings.Join(parts, ",")
}

// ja3Sum is the canonical lower-case-hex MD5 wrapper.
func ja3Sum(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // JA3 spec mandates MD5
	return hex.EncodeToString(sum[:])
}

// joinUint16 produces "a-b-c" from {a,b,c} in decimal. Empty list
// becomes the empty string, which JA3 represents literally.
func joinUint16(in []uint16) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, len(in))
	for i, v := range in {
		parts[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(parts, "-")
}

// joinUint8 is joinUint16 for the ec_point_formats list (uint8 in spec).
func joinUint8(in []uint8) string {
	if len(in) == 0 {
		return ""
	}
	parts := make([]string, len(in))
	for i, v := range in {
		parts[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(parts, "-")
}

// stripGreaseUint16 removes GREASE values per RFC 8701. GREASE
// reserves a small set of values that clients deliberately rotate to
// keep the protocol extensible; including them in a fingerprint
// would make every connection appear unique.
//
// GREASE values: 0x0A0A, 0x1A1A, 0x2A2A, ..., 0xFAFA — i.e. values
// where both bytes equal a hex digit pair `_A` (0x0A, 0x1A, ... 0xFA).
func stripGreaseUint16(in []uint16) []uint16 {
	out := in[:0:0] // new backing array, len 0
	for _, v := range in {
		if isGREASE16(v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// isGREASE16 reports whether a 16-bit value is a RFC 8701 GREASE
// value. The published table is exactly:
//
//	0x0A0A, 0x1A1A, 0x2A2A, 0x3A3A, 0x4A4A, 0x5A5A, 0x6A6A, 0x7A7A,
//	0x8A8A, 0x9A9A, 0xAAAA, 0xBABA, 0xCACA, 0xDADA, 0xEAEA, 0xFAFA
func isGREASE16(v uint16) bool {
	hi := uint8(v >> 8)
	lo := uint8(v)
	return hi == lo && (hi&0x0F) == 0x0A
}
