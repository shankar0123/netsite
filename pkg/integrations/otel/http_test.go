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
	"net/http/httptest"
	"testing"
)

// TestMiddleware_PassesThroughResponse asserts the wrapper does not
// alter the response a wrapped handler produces. The OTel layer is
// observability-only; behavior changes would be a regression.
func TestMiddleware_PassesThroughResponse(t *testing.T) {
	want := "ok"
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(want))
	})
	handler := Middleware("GET /v1/test", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	handler.ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusTeapot {
		t.Errorf("status = %d; want %d", got, http.StatusTeapot)
	}
	if got := rec.Header().Get("X-Custom"); got != "yes" {
		t.Errorf("header X-Custom = %q; want %q", got, "yes")
	}
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q; want %q", got, want)
	}
}

// TestMiddlewareFor_PassesThroughResponse asserts the HandlerFunc
// convenience wrapper has identical behaviour to Middleware.
func TestMiddlewareFor_PassesThroughResponse(t *testing.T) {
	handler := MiddlewareFor("GET /v1/test", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hi"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	if rec.Body.String() != "hi" {
		t.Errorf("body = %q; want %q", rec.Body.String(), "hi")
	}
}
