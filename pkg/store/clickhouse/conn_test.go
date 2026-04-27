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

package clickhouse

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

// TestOpen_EmptyDSN asserts that calling Open with an empty DSN returns
// the ErrEmptyDSN sentinel. Configuration validators rely on this so
// they can give precise feedback ("you didn't set
// NETSITE_CONTROLPLANE_CH_URL") instead of substring-matching the
// error string.
func TestOpen_EmptyDSN(t *testing.T) {
	conn, err := Open(context.Background(), "")
	if conn != nil {
		t.Fatalf("Open(\"\") returned non-nil conn")
	}
	if !errors.Is(err, ErrEmptyDSN) {
		t.Fatalf("Open(\"\") err = %v; want ErrEmptyDSN", err)
	}
}

// TestOpen_BadDSN asserts that Open with a syntactically invalid DSN
// returns a wrapped clickhouse.ParseDSN error rather than panicking.
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
			conn, err := Open(context.Background(), tc.dsn)
			if conn != nil {
				t.Fatalf("Open returned non-nil conn for %q", tc.dsn)
			}
			if err == nil {
				t.Fatalf("Open returned nil error for %q", tc.dsn)
			}
		})
	}
}

// TestSchema_ExposesEmbeddedFiles asserts the embedded schema FS
// surfaces at least 0001_init.sql. Catches drift where the //go:embed
// directive points at the wrong path or a file was renamed without
// updating the embed target.
func TestSchema_ExposesEmbeddedFiles(t *testing.T) {
	files, err := listSQLFiles(Schema())
	if err != nil {
		t.Fatalf("listSQLFiles(Schema()) err: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("Schema() exposed zero .sql files; expected at least 0001_init.sql")
	}
	if files[0] != "0001_init.sql" {
		t.Fatalf("first schema file = %q; expected %q", files[0], "0001_init.sql")
	}
}

// TestListSQLFiles_LexOrder asserts the applier orders files
// lexicographically.
func TestListSQLFiles_LexOrder(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_b.sql":    {Data: []byte("-- b")},
		"0002_a.sql":    {Data: []byte("-- a")},
		"0001_init.sql": {Data: []byte("-- init")},
		"README.md":     {Data: []byte("# not sql")},
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

// TestApply_NilArguments asserts the applier refuses nil conn or nil
// src with a clear error. The nil-conn branch fires first so the
// nil-src branch is exercised only when conn is non-nil; that case
// requires a real driver.Conn (testcontainers), and lives in
// schema_test.go under the integration build tag.
func TestApply_NilConn(t *testing.T) {
	err := Apply(context.Background(), nil, Schema())
	if err == nil {
		t.Fatal("Apply(ctx, nil, Schema()) returned nil error; want nil-conn error")
	}
	if !contains(err.Error(), "nil conn") {
		t.Errorf("error = %q; want substring %q", err.Error(), "nil conn")
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
