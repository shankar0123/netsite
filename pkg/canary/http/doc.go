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

// Package http implements the HTTP canary Runner.
//
// What: GET (or configurable verb) the target URL and record DNS,
// connect, TLS, TTFB, and total wall-clock timings, plus the response
// status code.
//
// How: stdlib `net/http` + `net/http/httptrace`. httptrace is the
// only stdlib mechanism that exposes per-phase timings; using it
// lets us avoid taking on a heavier dependency just to measure
// what's already in the standard library.
//
// Why one Runner type rather than a free function: the Runner
// holds the http.Client (with shared transport, timeouts, optional
// TLS config) so successive Run calls reuse connections where
// keep-alives apply. POPs canary the same target every 30s; a fresh
// Client per call would defeat keep-alive entirely.
package http
