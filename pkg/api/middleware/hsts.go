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

package middleware

import "net/http"

// What: HSTS (HTTP Strict-Transport-Security) middleware. Sets the
// `Strict-Transport-Security` header on every response so a
// browser that has spoken to us once over TLS will refuse to
// downgrade to plain HTTP for subsequent connections.
//
// How: a tiny wrapper that writes the header, then delegates. We
// only mount HSTS in the TLS-listen branch of api.Server.Run; in
// plaintext-allowed mode the header would mislead a browser that
// has just contacted us over HTTP.
//
// Why one year + includeSubDomains: HSTS is most useful when the
// browser remembers it across the operator's full domain. One year
// is the common production value (max-age=31536000) and
// includeSubDomains protects sub-zones from inadvertent plaintext
// fallback. We deliberately do not set `preload` because that
// requires submission to browser preload lists, which is the
// operator's deployment decision, not ours. Operators who want
// preload status add their own middleware in front.
//
// References:
//   - RFC 6797 — HSTS specification.
//   - https://hstspreload.org — preload list submission criteria.

const hstsHeaderValue = "max-age=31536000; includeSubDomains"

// HSTS wraps next with a handler that sets the Strict-Transport-
// Security header on every response. Only mount this in TLS-listen
// mode; running it in plain HTTP mode would tell browsers to refuse
// the very server they just connected to over HTTP.
func HSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", hstsHeaderValue)
		next.ServeHTTP(w, r)
	})
}
