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

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// What: opaque session ID generation and HTTP cookie helpers.
//
// How: a session ID is the prefix "ses-" followed by 32 hex characters
// (16 bytes from crypto/rand, hex-encoded). The full string is what
// the browser carries; the server looks it up against the sessions
// table. No claims, no signature — the ID is opaque.
//
// Why opaque IDs and not JWTs: opaque sessions are server-side
// revokable in O(1) (DELETE FROM sessions WHERE id = $1). Stateless
// JWTs cannot be revoked without a deny-list, which reintroduces the
// stateful lookup we were trying to avoid. NetSite is not a federated
// system; the control plane already has Postgres on the request path,
// so a server-side lookup is free.
//
// Why 16 bytes (128 bits) of entropy: at 16 bytes a brute-force search
// over the keyspace is computationally infeasible. Larger IDs (32 bytes
// = 256 bits) buy nothing and just enlarge cookies. NIST SP 800-63B
// recommends ≥128 bits for session identifiers.

// SessionCookieName is the HTTP cookie name carrying the session ID.
// The "__Host-" prefix in production should be considered (Phase 5)
// for stricter cookie semantics; in dev we use a simple name.
const SessionCookieName = "ns_session"

// idPrefixSession is the human-readable label prepended to every
// session ID so logs and database rows are unambiguously identifiable.
const idPrefixSession = "ses"

// idPrefixUser is the prefix for user IDs (usr-<short>).
const idPrefixUser = "usr"

// idPrefixTenant is the prefix for tenant IDs (tnt-<slug>) — kept here
// for consistency with the rest of the prefix set, though tenant IDs
// are typically slug-derived rather than randomly generated.
const idPrefixTenant = "tnt" //nolint:unused // referenced by repo.go on tenant insertion path

// NewSessionID returns a fresh, cryptographically random session ID.
// Returns an error only if the OS RNG is unavailable, which on Linux
// means /dev/urandom is missing — a fatal condition we surface
// faithfully rather than silently fall back.
func NewSessionID() (string, error) {
	return newPrefixedID(idPrefixSession)
}

// NewUserID returns a fresh user ID with the "usr-" prefix.
func NewUserID() (string, error) {
	return newPrefixedID(idPrefixUser)
}

// newPrefixedID is the shared body of NewSessionID and NewUserID.
// Returns "<prefix>-<32 hex chars>".
func newPrefixedID(prefix string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf[:]), nil
}

// SetSessionCookie writes a Set-Cookie header carrying the session ID
// with sane defaults: HttpOnly (defeats trivial XSS), SameSite=Lax
// (defeats most CSRF without breaking top-level navigations), Secure
// (TLS-only). The cookie expires at the session's ExpiresAt.
//
// In dev (no TLS) the Secure flag prevents Chrome from sending the
// cookie back to localhost over plain HTTP. Operators bringing up the
// dev compose stack must use http://localhost (which Chrome treats as
// "secure context") or accept that their first browser test needs
// http://localhost rather than 127.0.0.1.
func SetSessionCookie(w http.ResponseWriter, sess Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(time.Until(sess.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie writes a Set-Cookie that immediately expires the
// session cookie. Used by Logout so the browser forgets the ID even
// though the server has already deleted the record.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// SessionIDFromRequest extracts the session ID from the cookie on r.
// Returns "" if the cookie is missing or empty — handlers treat that
// as "anonymous" rather than as an error.
func SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
