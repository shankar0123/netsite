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

// Package annotations is NetSite's pinned-note primitive. Operators
// attach a tiny markdown body to a (scope, scope_id, timestamp)
// tuple — typically a canary failure point, a POP-wide event, or a
// test-config change. The dashboard renders annotations as markers
// on whichever timeline matches their scope.
//
// What:
//   - Annotation: id + tenant + (scope, scope_id, at) + author +
//     body_md + created_at.
//   - Scope: a small enum identifying the kind of object the
//     annotation hangs off (canary | pop | test in v0.0.12; more
//     scopes added per phase as they ship).
//   - Validate / sentinel errors for handler-side fast-fail.
//
// How: types.go declares the model; store.go is the pgxpool-backed
// CRUD. Service-layer logic is so thin (mint id, validate, hand
// off) that we expose a small Service struct here in types.go
// rather than splitting it into its own file.
//
// Why immutable: an annotation's role is to record what an operator
// noted at a moment in time. Mutating the body would invalidate the
// audit trail. Operators correct typos by deleting + recreating.
package annotations
