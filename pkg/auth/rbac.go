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

package auth

import (
	"errors"
	"strings"
)

// What: a coarse-grained RBAC primitive that maps (Role, Action) to
// allow/deny.
//
// How: an Action is a free-form string the caller chooses
// (e.g. "tests:read", "tests:write"); the policy is a static map from
// Role to a set of allowed action verbs. Authorize returns nil for
// allow and ErrForbidden for deny.
//
// Why a string-based action vocabulary rather than a richer policy
// engine (Casbin, OPA): Phase 0 has three roles and a handful of
// resource categories. A string verb is enough. Wiring a policy engine
// at this scale costs a dependency, a configuration file, and a
// debugging surface for nothing. We revisit when the action vocabulary
// crosses ~30 verbs or when a customer needs per-tenant policy
// customization (Phase 5).
//
// Action grammar: "<resource>:<verb>" where verb ∈ {read, write,
// admin}. Examples:
//
//   tests:read       - viewer, operator, admin
//   tests:write      - operator, admin
//   tenants:admin    - admin
//
// The grammar is documented (here, in the OpenAPI spec, in the route
// table) so callers and reviewers can predict authorization decisions
// without reading rbac.go.

// ErrForbidden is returned by Authorize when the role does not have
// the requested action. Handlers map this to HTTP 403.
var ErrForbidden = errors.New("auth: forbidden")

// verbLevel returns a numeric ordering on verbs so we can express
// "admin implies write implies read" without enumerating every
// (verb, role) cell. Higher number = more privileged.
func verbLevel(verb string) int {
	switch strings.ToLower(verb) {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	default:
		return 0 // unknown verb is treated as more privileged than any
	}
}

// roleLevel returns a numeric ordering on roles. Higher = more
// privileged. The mapping follows the verb levels:
//
//	viewer    → can read
//	operator  → can read, write
//	admin     → can read, write, admin
func roleLevel(r Role) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// Authorize reports whether r is permitted to perform action against
// the named resource. Returns nil on allow, ErrForbidden on deny.
//
// action is "<resource>:<verb>"; resourceHint is unused at v0 but
// reserved for per-resource overrides (Phase 5 will gain "this admin
// is admin only of tenant X" semantics).
func Authorize(r Role, action string) error {
	parts := strings.SplitN(action, ":", 2)
	if len(parts) != 2 {
		// Malformed action means the developer wrote a bad string.
		// Fail closed so the bug is visible in tests instead of
		// quietly handing out a permission.
		return ErrForbidden
	}
	verb := parts[1]
	if roleLevel(r) >= verbLevel(verb) && roleLevel(r) > 0 && verbLevel(verb) > 0 {
		return nil
	}
	return ErrForbidden
}

// CanRead returns true if role can read the resource. Convenience for
// route handlers that just want a boolean.
func CanRead(r Role, resource string) bool {
	return Authorize(r, resource+":read") == nil
}

// CanWrite returns true if role can write the resource.
func CanWrite(r Role, resource string) bool {
	return Authorize(r, resource+":write") == nil
}

// CanAdmin returns true if role can admin the resource.
func CanAdmin(r Role, resource string) bool {
	return Authorize(r, resource+":admin") == nil
}
