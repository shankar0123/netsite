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

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// TestLogging_RecordsStatusAndPath asserts the logging middleware
// emits a structured log line containing the method, path, and the
// response status produced by the wrapped handler.
func TestLogging_RecordsStatusAndPath(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})
	h := Logging(newTestLogger(&buf), inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/teapot", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d; want 418", rec.Code)
	}
	got := buf.String()
	cases := []struct {
		name string
		want string
	}{
		{"method recorded", "method=GET"},
		{"path recorded", "path=/v1/teapot"},
		{"status recorded", "status=418"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(got, tc.want) {
				t.Errorf("log %q missing %q", got, tc.want)
			}
		})
	}
}

// TestLogging_DefaultStatus200 covers the case where the inner handler
// writes a body without explicitly calling WriteHeader. The recorder
// promotes the implicit 200 to a logged status of 200.
func TestLogging_DefaultStatus200(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := Logging(newTestLogger(&buf), inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(buf.String(), "status=200") {
		t.Errorf("expected status=200 in log; got %q", buf.String())
	}
}

// TestRecover_PanicYields500 asserts the recover middleware turns a
// panic in a handler into a logged 500 instead of letting the stdlib
// http.Server kill the connection.
func TestRecover_PanicYields500(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	h := Recover(newTestLogger(&buf), inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "panic=boom") {
		t.Errorf("expected panic=boom in log; got %q", buf.String())
	}
}

// TestRecover_NoPanicPassthrough asserts the wrapper does not alter
// the response when the inner handler does not panic.
func TestRecover_NoPanicPassthrough(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})
	h := Recover(newTestLogger(&buf), inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d; want 201", rec.Code)
	}
	if rec.Body.String() != "created" {
		t.Errorf("body = %q; want %q", rec.Body.String(), "created")
	}
}
