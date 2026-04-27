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

package otel

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// What: stdlib net/http middleware that emits a server span per request.
//
// How: wraps any http.Handler with otelhttp.NewHandler. The wrapper
// extracts W3C Trace Context from request headers (so cross-service
// traces stitch together), starts a server span named after the
// supplied operation string, and ends it on response.
//
// Why an exported Middleware function rather than callers using
// otelhttp directly: keeps the otelhttp import surface localized to
// this package. If we ever need to add NetSite-specific span
// attributes (tenant_id, request_id propagation), it lands here once
// instead of at every call site.
//
// Why supply an operation name rather than infer from the route:
// Cobra-style stdlib http.ServeMux does not expose route patterns at
// runtime; callers know the route they are wrapping. Using the
// operation name keeps span names stable across deploys, which keeps
// dashboards stable.

// Middleware wraps handler and emits a server span per request named
// "operation". Use it on each registered route — typically the value
// is the route pattern, e.g. "GET /v1/health".
func Middleware(operation string, handler http.Handler) http.Handler {
	return otelhttp.NewHandler(handler, operation)
}

// MiddlewareFor wraps a HandlerFunc directly, saving callers from the
// type assertion. Equivalent to:
//
//	Middleware(op, http.HandlerFunc(fn))
func MiddlewareFor(operation string, fn http.HandlerFunc) http.Handler {
	return Middleware(operation, fn)
}
