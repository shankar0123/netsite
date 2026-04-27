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
	"crypto/md5" //nolint:gosec // matches JA3 spec
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestJA3String_FromKnownFingerprint walks the JA3 string-construction
// rules through a small representative ClientHelloFingerprint. The
// expected string is hand-derived from the spec at
// https://github.com/salesforce/ja3 and serves as a regression vector.
func TestJA3String_FromKnownFingerprint(t *testing.T) {
	f := ClientHelloFingerprint{
		Version:                   771, // 0x0303 = TLS 1.2
		Ciphers:                   []uint16{49195, 49199, 158},
		Extensions:                []uint16{0, 23, 65281, 10, 11, 35, 16, 5, 13, 28, 21},
		EllipticCurves:            []uint16{29, 23, 24},
		EllipticCurvePointFormats: []uint8{0},
	}
	want := "771,49195-49199-158,0-23-65281-10-11-35-16-5-13-28-21,29-23-24,0"
	if got := f.JA3String(); got != want {
		t.Errorf("JA3String mismatch:\n  got  %s\n  want %s", got, want)
	}
}

// TestJA3_HashMatchesMD5OfString asserts the JA3 hash equals the MD5
// of the JA3 string. Two checks for the price of one.
func TestJA3_HashMatchesMD5OfString(t *testing.T) {
	f := ClientHelloFingerprint{
		Version:                   771,
		Ciphers:                   []uint16{49195, 49199, 158},
		Extensions:                []uint16{0, 23, 65281, 10, 11, 35, 16, 5, 13, 28, 21},
		EllipticCurves:            []uint16{29, 23, 24},
		EllipticCurvePointFormats: []uint8{0},
	}
	str := f.JA3String()
	expected := md5HexLower(str)
	if got := f.JA3(); got != expected {
		t.Errorf("JA3() = %s; want md5(JA3String) = %s", got, expected)
	}
}

// TestJA3_GREASEStripped asserts that GREASE values in the input
// are stripped before serialisation. Adding GREASE to the cipher
// list should NOT change the hash.
func TestJA3_GREASEStripped(t *testing.T) {
	base := ClientHelloFingerprint{
		Version:                   771,
		Ciphers:                   []uint16{49195, 49199, 158},
		Extensions:                []uint16{0, 23},
		EllipticCurves:            []uint16{29, 23},
		EllipticCurvePointFormats: []uint8{0},
	}
	withGrease := base
	withGrease.Ciphers = append([]uint16{0xAAAA}, base.Ciphers...) // prepend GREASE
	withGrease.Extensions = append([]uint16{0x0A0A}, base.Extensions...)

	if base.JA3() != withGrease.JA3() {
		t.Errorf("GREASE altered JA3 hash: base=%s grease=%s", base.JA3(), withGrease.JA3())
	}
}

// TestJA3_ZeroIsEmpty asserts the zero-value fingerprint produces "".
func TestJA3_ZeroIsEmpty(t *testing.T) {
	if got := (ClientHelloFingerprint{}).JA3(); got != "" {
		t.Errorf("zero JA3() = %q; want empty", got)
	}
}

// TestIsGREASE16 covers all 16 published GREASE values plus a few
// adjacent non-GREASE values that look similar.
func TestIsGREASE16(t *testing.T) {
	expectGrease := []uint16{
		0x0A0A, 0x1A1A, 0x2A2A, 0x3A3A, 0x4A4A, 0x5A5A, 0x6A6A, 0x7A7A,
		0x8A8A, 0x9A9A, 0xAAAA, 0xBABA, 0xCACA, 0xDADA, 0xEAEA, 0xFAFA,
	}
	for _, v := range expectGrease {
		if !isGREASE16(v) {
			t.Errorf("isGREASE16(0x%04X) = false; want true", v)
		}
	}
	expectNot := []uint16{0x0000, 0x0A0B, 0xAAAB, 0x1234}
	for _, v := range expectNot {
		if isGREASE16(v) {
			t.Errorf("isGREASE16(0x%04X) = true; want false", v)
		}
	}
}

// TestJA4_StructureAndComponents asserts the JA4 string has the
// expected three-part shape and that each part has the documented
// length. Treats the algorithm as black-box: we build a fingerprint,
// observe the output, and verify every spec invariant individually.
func TestJA4_StructureAndComponents(t *testing.T) {
	f := ClientHelloFingerprint{
		Version:    0x0303, // TLS 1.2
		SNIPresent: true,
		Ciphers:    []uint16{0x1301, 0x1302, 0xC02F},
		Extensions: []uint16{
			0x0000, 0x0010, 0x0023, 0x000B, 0x000A,
		},
		SignatureAlgorithms: []uint16{0x0403, 0x0804},
		ALPN:                []string{"h2", "http/1.1"},
	}
	out := f.JA4()
	parts := strings.Split(out, "_")
	if len(parts) != 3 {
		t.Fatalf("JA4 = %q; want 3 underscore-separated parts", out)
	}
	if len(parts[0]) != 10 {
		t.Errorf("ja4_a length = %d; want 10 (%q)", len(parts[0]), parts[0])
	}
	if len(parts[1]) != 12 {
		t.Errorf("ja4_b length = %d; want 12 (%q)", len(parts[1]), parts[1])
	}
	if len(parts[2]) != 12 {
		t.Errorf("ja4_c length = %d; want 12 (%q)", len(parts[2]), parts[2])
	}

	// ja4_a: t (TCP), 12 (TLS 1.2), d (SNI present), 03 (3 ciphers
	// after grease strip), 05 (5 extensions before SNI/ALPN
	// exclusion — JA4's count is BEFORE the JA4-C exclusion), h2
	// (first ALPN: first byte 'h', last byte '2').
	if parts[0] != "t12d0305h2" {
		t.Errorf("ja4_a = %q; want %q", parts[0], "t12d0305h2")
	}

	// ja4_b: SHA-256 prefix of "4865,4866,49199" (sorted decimals).
	wantB := sha256Prefix("4865,4866,49199")
	if parts[1] != wantB {
		t.Errorf("ja4_b = %q; want %q", parts[1], wantB)
	}
}

// TestVersionString covers the TLS-version → 2-char encoding.
func TestVersionString(t *testing.T) {
	cases := map[uint16]string{
		0x0304: "13", 0x0303: "12", 0x0302: "11", 0x0301: "10",
		0x0000: "00", 0xDEAD: "00",
	}
	for in, want := range cases {
		if got := versionString(in); got != want {
			t.Errorf("versionString(0x%04X) = %q; want %q", in, got, want)
		}
	}
}

// TestFirstALPN exercises the first/last-byte ALPN encoding.
func TestFirstALPN(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, "00"},
		{"empty string", []string{""}, "00"},
		{"h2", []string{"h2"}, "h2"},
		{"http/1.1", []string{"http/1.1"}, "h1"},
		{"non-printable", []string{"\x00\x01"}, "00"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := firstALPN(tc.in); got != tc.want {
				t.Errorf("firstALPN(%v) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// md5HexLower is the JA3 spec's "MD5(s) as 32 hex chars lower-case".
// Defined here so the test file does not import md5 indirectly via
// the package under test.
func md5HexLower(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // JA3 spec mandates MD5
	return hex.EncodeToString(sum[:])
}

// sha256Prefix is the test-side equivalent of sha256Prefix12.
func sha256Prefix(s string) string {
	if s == "" {
		return "000000000000"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
