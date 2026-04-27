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

// Package middleware holds stdlib net/http middleware used by every
// NetSite HTTP server (control plane, POP debug surfaces, RUM
// ingestion). Each piece is a `func(http.Handler) http.Handler`
// composer so callers wrap with whatever order they want.
//
// What ships in v0.0.x:
//   - Logging: slog-backed access log with request id, method, path,
//     status, duration, remote addr.
//   - Recover: panics → 500 with the stack written to slog at Error.
//   - Authenticate: resolves the session cookie to an auth.User and
//     attaches it to the request context. Anonymous requests pass
//     through; route gating is the next layer's job.
//   - Authorize: declarative RBAC gate keyed on action strings (per
//     pkg/auth.Authorize). 401 for anonymous, 403 for forbidden.
//   - HSTS: sets Strict-Transport-Security in TLS-listen mode only.
//     Mounting it in plaintext mode would tell browsers "always use
//     TLS" against a server that just answered over plain HTTP — a
//     trap. The api.Server only mounts HSTS when TLSEnabled().
//
// Why no router-shaped framework on top of these: the api package
// uses stdlib http.ServeMux and registers routes explicitly. The
// middleware here composes by wrapping. That's enough for v0.x; if
// we ever feel the pull toward a router with route-scoped middleware
// (e.g. Chi's per-group Use), we revisit then.
package middleware
