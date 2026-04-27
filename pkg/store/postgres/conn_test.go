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

package postgres

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

// TestOpen_EmptyDSN asserts that calling Open with an empty DSN returns
// the ErrEmptyDSN sentinel. Configuration validators rely on this so
// they can give precise feedback ("you didn't set NETSITE_CONTROLPLANE_DB_URL")
// instead of a string-matched error.
func TestOpen_EmptyDSN(t *testing.T) {
	pool, err := Open(context.Background(), "")
	if pool != nil {
		t.Fatalf("Open(\"\") returned non-nil pool")
	}
	if !errors.Is(err, ErrEmptyDSN) {
		t.Fatalf("Open(\"\") err = %v; want ErrEmptyDSN", err)
	}
}

// TestOpen_BadDSN asserts that Open with a syntactically invalid DSN
// returns a wrapped pgxpool.ParseConfig error rather than panicking.
// The wrapping is important so callers can match on errors.Is /
// errors.As instead of substring-matching the error string.
func TestOpen_BadDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
	}{
		{"unknown scheme", "memcached://nope"},
		{"malformed url", "://broken"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pool, err := Open(context.Background(), tc.dsn)
			if pool != nil {
				t.Fatalf("Open returned non-nil pool for %q", tc.dsn)
			}
			if err == nil {
				t.Fatalf("Open returned nil error for %q", tc.dsn)
			}
		})
	}
}

// TestMigrations_ExposesEmbeddedFiles asserts that the embedded
// migrations FS surfaces at least 0001_init.sql. Catches drift where
// the //go:embed directive points at the wrong path or a migration was
// renamed without updating the embed target.
func TestMigrations_ExposesEmbeddedFiles(t *testing.T) {
	files, err := listSQLFiles(Migrations())
	if err != nil {
		t.Fatalf("listSQLFiles(Migrations()) err: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("Migrations() exposed zero .sql files; expected at least 0001_init.sql")
	}
	if files[0] != "0001_init.sql" {
		t.Fatalf("first migration = %q; expected %q", files[0], "0001_init.sql")
	}
}

// TestListSQLFiles_LexOrder asserts the runner orders files
// lexicographically, which matches numeric ordering for zero-padded
// prefixes per the migrations README.
func TestListSQLFiles_LexOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_b.sql":    {Data: []byte("-- b")},
		"0002_a.sql":    {Data: []byte("-- a")},
		"0001_init.sql": {Data: []byte("-- init")},
		"README.md":     {Data: []byte("# not sql")},
		"sub/dir/x":     {Data: []byte("not at root")},
	}
	got, err := listSQLFiles(fsys)
	if err != nil {
		t.Fatalf("listSQLFiles err: %v", err)
	}
	want := []string{"0001_init.sql", "0002_a.sql", "0010_b.sql"}
	if len(got) != len(want) {
		t.Fatalf("listSQLFiles returned %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q; want %q", i, got[i], want[i])
		}
	}
}

// TestListSQLFiles_IgnoresNonSQL asserts the runner skips files that
// are not *.sql. The migrations directory will accumulate a README and
// possibly editor scratch files; those must never be applied.
func TestListSQLFiles_IgnoresNonSQL(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_init.sql":     {Data: []byte("-- init")},
		"README.md":         {Data: []byte("# rules")},
		"0001_init.sql.bak": {Data: []byte("-- editor backup")},
	}
	got, err := listSQLFiles(fsys)
	if err != nil {
		t.Fatalf("listSQLFiles err: %v", err)
	}
	if len(got) != 1 || got[0] != "0001_init.sql" {
		t.Fatalf("listSQLFiles = %v; want [0001_init.sql]", got)
	}
}

// TestMigrate_NilPool asserts the runner refuses a nil pool with a
// clear error rather than panicking with a nil-pointer deref. The
// nil-src branch needs a non-nil *pgxpool.Pool to reach it, which means
// it requires testcontainers; that branch is exercised in
// migrate_test.go under the integration build tag.
func TestMigrate_NilPool(t *testing.T) {
	err := Migrate(context.Background(), nil, Migrations())
	if err == nil {
		t.Fatal("Migrate(ctx, nil, Migrations()) returned nil error; want nil-pool error")
	}
	if !contains(err.Error(), "nil pool") {
		t.Errorf("error = %q; want substring %q", err.Error(), "nil pool")
	}
}

// contains is strings.Contains kept inline so this test file does not
// re-import the strings package only for one call.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
