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
	"testing"
)

// TestAuthorize_Matrix exercises every (role, action) cell. The
// matrix is the canonical statement of policy; if behavior diverges
// from this table, either the table is wrong or rbac.go is.
func TestAuthorize_Matrix(t *testing.T) {
	cases := []struct {
		role    Role
		action  string
		allowed bool
	}{
		// viewer
		{RoleViewer, "tests:read", true},
		{RoleViewer, "tests:write", false},
		{RoleViewer, "tests:admin", false},
		{RoleViewer, "tenants:admin", false},

		// operator
		{RoleOperator, "tests:read", true},
		{RoleOperator, "tests:write", true},
		{RoleOperator, "tests:admin", false},
		{RoleOperator, "tenants:admin", false},

		// admin
		{RoleAdmin, "tests:read", true},
		{RoleAdmin, "tests:write", true},
		{RoleAdmin, "tests:admin", true},
		{RoleAdmin, "tenants:admin", true},

		// unknown role denies everything
		{Role("intern"), "tests:read", false},
		{Role(""), "tests:read", false},

		// malformed actions deny
		{RoleAdmin, "no_colon_here", false},
		{RoleAdmin, ":empty_resource", false},
		{RoleAdmin, "empty_verb:", false},
	}
	for _, tc := range cases {
		tc := tc
		name := string(tc.role) + " / " + tc.action
		t.Run(name, func(t *testing.T) {
			err := Authorize(tc.role, tc.action)
			got := err == nil
			if got != tc.allowed {
				t.Errorf("Authorize(%q, %q) allowed = %v; want %v (err = %v)", tc.role, tc.action, got, tc.allowed, err)
			}
			if !tc.allowed && err != nil && !errors.Is(err, ErrForbidden) {
				t.Errorf("deny err = %v; want ErrForbidden", err)
			}
		})
	}
}

// TestCanRead_CanWrite_CanAdmin asserts the convenience wrappers
// produce the same result as Authorize directly.
func TestCanRead_CanWrite_CanAdmin(t *testing.T) {
	cases := []struct {
		role     Role
		resource string
		read     bool
		write    bool
		admin    bool
	}{
		{RoleViewer, "x", true, false, false},
		{RoleOperator, "x", true, true, false},
		{RoleAdmin, "x", true, true, true},
	}
	for _, tc := range cases {
		tc := tc
		name := string(tc.role) + " / " + tc.resource
		t.Run(name, func(t *testing.T) {
			if got := CanRead(tc.role, tc.resource); got != tc.read {
				t.Errorf("CanRead = %v; want %v", got, tc.read)
			}
			if got := CanWrite(tc.role, tc.resource); got != tc.write {
				t.Errorf("CanWrite = %v; want %v", got, tc.write)
			}
			if got := CanAdmin(tc.role, tc.resource); got != tc.admin {
				t.Errorf("CanAdmin = %v; want %v", got, tc.admin)
			}
		})
	}
}
