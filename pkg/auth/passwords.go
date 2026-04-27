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
	"fmt"
	"os"
	"strconv"

	"golang.org/x/crypto/bcrypt"
)

// What: bcrypt password hashing and verification.
//
// How: HashPassword reads the cost knob from NETSITE_AUTH_BCRYPT_COST
// (default 12) and produces a bcrypt-encoded hash. VerifyPassword
// compares a candidate against a stored hash in constant time relative
// to the password length, as guaranteed by `bcrypt.CompareHashAndPassword`.
//
// Why bcrypt at cost 12 (not Argon2id, not scrypt): bcrypt is the
// stdlib-adjacent default, fully supported in golang.org/x/crypto,
// and cost 12 puts a single hash at ~250ms on commodity hardware.
// That's adequate for a self-hosted single-server control plane with
// at most O(seconds) of login churn. Argon2id is technically stronger
// but adds a new dependency for marginal gain at v0 scale; we revisit
// if the threat model changes (Phase 5).
//
// The default cost can be overridden at boot time without recompiling.
// Operators on weaker hardware may set NETSITE_AUTH_BCRYPT_COST=10.
// Operators with vault-grade key management should leave the default.

// minBcryptCost is the floor we accept regardless of operator override.
// bcrypt.MinCost is 4, which is meaninglessly fast and would make a
// stolen dump trivially crackable. NIST recommends ≥10 for general
// use; we set the floor to 10.
const minBcryptCost = 10

// defaultBcryptCost is the cost used when NETSITE_AUTH_BCRYPT_COST is
// unset or empty. Tuned to ~250ms per hash on 2025-era M-series silicon.
const defaultBcryptCost = 12

// HashPassword returns a bcrypt hash of password using the configured
// cost. Returns ErrWeakPassword if password is shorter than
// MinPasswordLength.
//
// The returned string is the canonical bcrypt encoding (algorithm
// version + cost + salt + hash) and is safe to store directly in the
// users.password_hash TEXT column.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", ErrWeakPassword
	}
	cost := readBcryptCost()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("auth: bcrypt hash: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether candidate matches storedHash. It
// returns nil on success and a non-nil error on mismatch. Callers
// should NOT distinguish "wrong password" from "garbled hash" — both
// collapse to ErrInvalidCredentials at the Service layer to deny
// timing oracles.
//
// VerifyPassword is constant-time relative to the password's length
// per bcrypt's contract; the storedHash's encoded cost determines
// total work, not the password.
func VerifyPassword(storedHash, candidate string) error {
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(candidate))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrInvalidCredentials
	}
	return fmt.Errorf("auth: bcrypt verify: %w", err)
}

// readBcryptCost honors NETSITE_AUTH_BCRYPT_COST when set and parseable,
// clamps to minBcryptCost as a floor, and falls back to defaultBcryptCost
// when unset, empty, or unparseable.
func readBcryptCost() int {
	v, ok := os.LookupEnv("NETSITE_AUTH_BCRYPT_COST")
	if !ok || v == "" {
		return defaultBcryptCost
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultBcryptCost
	}
	if n < minBcryptCost {
		return minBcryptCost
	}
	if n > bcrypt.MaxCost {
		return bcrypt.MaxCost
	}
	return n
}
