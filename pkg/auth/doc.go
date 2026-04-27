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

// Package auth implements local-password authentication, opaque
// cookie-based sessions, and role-based access control for NetSite.
//
// What: a Service that exposes Login/Logout/Whoami/CreateUser/
// RotatePassword backed by Postgres tables created in migration
// 0003_auth_core.sql. Plus an RBAC primitive (Role + Authorize) that
// the API middleware consumes to gate routes.
//
// How: passwords use bcrypt; session IDs are 16 random bytes prefixed
// with "ses-"; both halves of the auth flow (passwords and sessions)
// live behind a Service interface so handlers receive an abstract
// dependency they can mock in tests.
//
// Why local-only in Phase 0 (per PRD D11): OIDC and SSO are real but
// large. Phase 0's remit is "deploy 3 POPs and run canaries"; bringing
// up an Identity Provider tax is incompatible with that. Local users
// give us the auth surface every later feature needs (RBAC, session
// cookies, an authenticated context) without the deployment pain.
// OIDC lands at the end of Phase 1; full SSO/SAML in Phase 5.
package auth
