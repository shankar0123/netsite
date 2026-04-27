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
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// What: an idempotent SQL migration runner.
//
// How: each migration is a file under migrations/ named with a numeric
// prefix and a description (e.g. 0001_init.sql). Migrate() applies
// pending files in lexicographic order. Successfully applied names are
// recorded in a _schema_migrations table. A second Migrate() call
// against the same database is a no-op because every applied file is
// skipped, and the SQL itself is required by convention to be idempotent
// (CREATE ... IF NOT EXISTS, ON CONFLICT DO NOTHING) so even a manually
// truncated tracking table cannot break things.
//
// Why not golang-migrate or goose: those tools impose CLI workflows,
// down-migrations, and version dependency models that NetSite does not
// need. Postgres migrations in this codebase are forward-only and
// idempotent. The 60 lines below replicate the only behavior we need
// from external tools without adding a dependency to the diligence
// dependency graph.

// migrationsFS holds the *.sql files under migrations/. The runner reads
// them at Migrate() time. Embedding rather than reading from disk means
// the binary ships with its schema baked in: a fresh deploy needs no
// SQL files on the filesystem.
//
//go:embed migrations
var migrationsFS embed.FS

// Migrations exposes the embedded migration set as an fs.FS rooted at
// the migrations directory. Callers (typically the control-plane main
// during boot) pass this to Migrate(). Tests can pass a different fs.FS
// — for example, a fstest.MapFS — to exercise edge cases without
// shipping placeholder SQL.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		// Embed paths are validated at compile time; an error here
		// would mean the embed directive itself is wrong, which is a
		// programming error, not a runtime condition.
		panic(fmt.Sprintf("postgres: embedded migrations subtree missing: %v", err))
	}
	return sub
}

// schemaMigrationsDDL creates the tracking table if it does not exist.
// Running it twice is a no-op. The applied_at column gives operators a
// trivial way to audit when the schema landed.
const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS _schema_migrations (
    name        TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies every *.sql file in src that has not yet been recorded
// in _schema_migrations against the given pool, in lexicographic order.
// Files are applied inside individual transactions; on failure, the
// in-flight migration is rolled back and the error is returned. Earlier
// migrations stay applied.
//
// Why one transaction per file (not one big transaction): some Postgres
// statements (CREATE INDEX CONCURRENTLY, ALTER TYPE for an enum) cannot
// run inside a transaction. Per-file transactions also make a partial
// failure recoverable: re-running Migrate() picks up where it stopped.
// The cost is that a multi-file change is not atomic across files; that
// is acceptable for forward-only schema evolution.
func Migrate(ctx context.Context, pool *pgxpool.Pool, src fs.FS) error {
	if pool == nil {
		return fmt.Errorf("postgres: Migrate called with nil pool")
	}
	if src == nil {
		return fmt.Errorf("postgres: Migrate called with nil src")
	}

	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("postgres: ensure _schema_migrations: %w", err)
	}

	applied, err := loadAppliedSet(ctx, pool)
	if err != nil {
		return err
	}

	files, err := listSQLFiles(src)
	if err != nil {
		return err
	}

	for _, name := range files {
		if _, ok := applied[name]; ok {
			continue
		}
		body, err := fs.ReadFile(src, name)
		if err != nil {
			return fmt.Errorf("postgres: read migration %q: %w", name, err)
		}
		if err := applyOne(ctx, pool, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// loadAppliedSet returns the set of migration names already recorded in
// _schema_migrations. Returning a map (rather than a slice) lets the
// caller do constant-time membership checks.
func loadAppliedSet(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM _schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query _schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: scan _schema_migrations: %w", err)
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate _schema_migrations: %w", err)
	}
	return applied, nil
}

// listSQLFiles returns the *.sql file names in src in lexicographic
// order. Lex order matches the convention NNNN_description.sql so
// ordering matches the operator's intent without us having to parse
// numeric prefixes.
func listSQLFiles(src fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("postgres: list migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	return files, nil
}

// applyOne runs body and records name in _schema_migrations within a
// single transaction.
func applyOne(ctx context.Context, pool *pgxpool.Pool, name, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx for %q: %w", name, err)
	}
	// Rollback is a no-op after Commit succeeds; safe to defer
	// unconditionally.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("postgres: apply %q: %w", name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO _schema_migrations(name) VALUES($1) ON CONFLICT DO NOTHING`,
		name,
	); err != nil {
		return fmt.Errorf("postgres: record %q applied: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit %q: %w", name, err)
	}
	return nil
}
