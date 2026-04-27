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

// Package api is the HTTP surface of the NetSite control plane.
//
// What: a stdlib `net/http` server with the canonical NetSite middleware
// stack (logging → recovery → OTel) and an explicit route table. No
// third-party HTTP framework — architecture invariant in CLAUDE.md.
//
// How: New() builds a *Server from a Config. Run(ctx) blocks until ctx
// is canceled, then performs a 30-second graceful shutdown. Routes are
// registered against an http.ServeMux at construction time; adding a
// new route is a single mux.Handle call.
//
// Why a struct (not a free `func ListenAndServe`): tests need to
// construct an isolated server, point it at a httptest.Server-equivalent,
// and exercise it. A struct gives the tests a handle.
package api
