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

// Package workspaces is NetSite's saved-view bundle. An operator
// pins a few views (netql queries, dashboard routes, raw URLs) and
// gives the bundle a name; opening the workspace later returns the
// page exactly as they left it.
//
// What:
//   - Workspace: a tenant-scoped, owner-attributed bundle of Views
//     plus optional sharing metadata.
//   - View: one entry — a name, a URL/deep-link, and an optional
//     note (one or two sentences explaining what the view is for).
//   - Service: the small business-logic layer that validates input,
//     mints prefixed IDs and share slugs, and enforces tenant
//     scoping on every read.
//
// How: types.go declares the shapes and validation; store.go is a
// thin pgxpool-backed CRUD layer; service.go composes the two with
// a Clock abstraction so tests stay deterministic. Handlers in
// pkg/api/workspaces.go translate HTTP requests into Service calls.
//
// Why a single-purpose package rather than folding workspaces into
// pkg/api: the same shapes are reused by the share-link resolver
// (which has no auth and lives at /v1/share/{slug}). Putting the
// data model and business logic in one package keeps both surfaces
// honest and lets us add CLI / RPC bindings later without touching
// the model.
package workspaces
