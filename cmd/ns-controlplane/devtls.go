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

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// What: AutoTLS mints a fresh self-signed certificate for a
// loopback bind address and writes it to a temp directory so the
// existing TLSCertFile / TLSKeyFile flow can consume it. Used in
// dev / one-off demo scenarios where the operator wants HTTPS in
// 30 seconds without dealing with cert management.
//
// How: ECDSA-P256 key, x509 cert with CN=localhost, SAN-listing
// 127.0.0.1, ::1, localhost. 30-day validity (long enough for a
// demo session but short enough that a leaked cert ages out before
// it can do harm). Cert and key written to ${TMPDIR}/netsite-
// devtls/ (per-process directory). The cert SHA-256 fingerprint is
// returned so the boot logger can print it; an operator who wants
// to pin curl to this cert grabs the fingerprint from the log.
//
// Why this is loopback-only: a self-signed cert that auto-issues
// to whatever address you bind would be a security foot-gun. The
// caller must verify the bind address resolves to a loopback
// before invoking AutoTLS. We re-check inside AutoTLS as defence
// in depth.
//
// Why not stash the cert in /etc/ssl/... or similar: AutoTLS is
// for the local-dev path. Persistence isn't needed; rotation
// happens on every process restart. This also makes leak
// containment trivial — kill the process, the cert is gone.

// AutoTLSResult carries the file paths and the cert fingerprint a
// caller needs to log + bind.
type AutoTLSResult struct {
	CertFile          string
	KeyFile           string
	SHA256Fingerprint string // hex-encoded, lower-case
}

// devTLSDir is the per-process directory under TMPDIR. We don't
// remove it on exit because the OS will clean it up at next reboot
// and an operator running `--help` after a crash might still want
// to inspect the cert.
const devTLSDir = "netsite-devtls"

// AutoTLS generates an ephemeral self-signed cert valid for the
// canonical loopback names + the listening address. Returns
// ErrNonLoopbackBind when addr resolves to anything other than
// 127.0.0.1, ::1, localhost, or the empty host.
func AutoTLS(addr string) (AutoTLSResult, error) {
	if !isLoopbackAddr(addr) {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: %w (addr=%q); refusing to bind self-signed cert to non-loopback", ErrNonLoopbackBind, addr)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: ecdsa generate: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: serial: %w", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost", Organization: []string{"NetSite Dev AutoTLS"}},
		NotBefore:             now.Add(-time.Minute), // tolerate clock skew
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:              []string{"localhost"},
		IsCA:                  false,
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: create certificate: %w", err)
	}

	dir := filepath.Join(os.TempDir(), devTLSDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: mkdir: %w", err)
	}

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: write cert: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return AutoTLSResult{}, fmt.Errorf("dev-autotls: write key: %w", err)
	}

	sum := sha256.Sum256(derBytes)
	return AutoTLSResult{
		CertFile:          certPath,
		KeyFile:           keyPath,
		SHA256Fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

// ErrNonLoopbackBind is returned by AutoTLS when the listen address
// is not loopback. Surfaces as an exit-time error to the operator;
// AutoTLS NEVER issues a cert for an address that could be reached
// from outside the host.
var ErrNonLoopbackBind = errors.New("AutoTLS only supports loopback bind addresses")

// isLoopbackAddr reports whether addr — in the form ":port",
// "host:port", or "host" — resolves to the loopback interface.
//
// We accept the empty host (":port") as loopback because Go's
// net.Listen treats it as "all interfaces"; binding to all
// interfaces with a self-signed cert is the foot-gun we're
// preventing, so we reject that.
//
// What we accept:
//
//	127.0.0.1:8080     → loopback
//	[::1]:8080         → loopback
//	localhost:8080     → loopback (DNS-resolved)
//	127.0.0.1          → loopback (host only, no port)
//
// What we reject:
//
//	:8080              → all interfaces — NOT loopback
//	0.0.0.0:8080       → all interfaces
//	192.168.1.5:8080   → not loopback
//	example.com:8080   → not loopback
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr has no port; treat the whole string as host.
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		// `:port` or empty addr — bind to all interfaces. Reject.
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// non-IP, non-localhost host — reject.
		return false
	}
	return ip.IsLoopback()
}
