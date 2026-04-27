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
	"testing"
)

// TestHashPassword_RoundTrip exercises the canonical hash → verify
// loop with a couple of representative passwords. We use the minimum-
// length values to keep bcrypt cost-12 inside the test budget.
func TestHashPassword_RoundTrip(t *testing.T) {
	t.Setenv("NETSITE_AUTH_BCRYPT_COST", "10") // keep test fast

	cases := []struct {
		name string
		pw   string
	}{
		{"min length", strings.Repeat("a", MinPasswordLength)},
		{"mixed", "Sup3rS3cret!Pass"},
		{"unicode", "пароль123456"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashPassword(tc.pw)
			if err != nil {
				t.Fatalf("HashPassword: %v", err)
			}
			if hash == tc.pw {
				t.Fatal("hash should not equal plaintext")
			}
			if err := VerifyPassword(hash, tc.pw); err != nil {
				t.Fatalf("VerifyPassword: %v", err)
			}
			// Wrong password.
			if err := VerifyPassword(hash, tc.pw+"x"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("VerifyPassword(wrong) err = %v; want ErrInvalidCredentials", err)
			}
		})
	}
}

// TestHashPassword_TooShort asserts the length floor fires.
func TestHashPassword_TooShort(t *testing.T) {
	hash, err := HashPassword("short")
	if hash != "" {
		t.Fatalf("HashPassword returned non-empty hash for short password")
	}
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("err = %v; want ErrWeakPassword", err)
	}
}

// TestReadBcryptCost asserts each branch of the env-var parser.
func TestReadBcryptCost(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{"unset", "", false, defaultBcryptCost},
		{"empty", "", true, defaultBcryptCost},
		{"unparseable", "abc", true, defaultBcryptCost},
		{"below floor", "8", true, minBcryptCost},
		{"in range", "12", true, 12},
		{"above max", "1000", true, 31}, // bcrypt.MaxCost
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("NETSITE_AUTH_BCRYPT_COST", tc.value)
			} else {
				// unset path — explicitly unset before reading.
				t.Setenv("NETSITE_AUTH_BCRYPT_COST", "")
			}
			if got := readBcryptCost(); got != tc.want {
				t.Errorf("readBcryptCost = %d; want %d", got, tc.want)
			}
		})
	}
}
