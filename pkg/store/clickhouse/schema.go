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
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// What: a forward-only, idempotent schema applier for ClickHouse.
//
// How: each schema file under schema/ is named NNNN_description.sql.
// Apply() runs unrecorded files in lex order, tracking applied names in
// a `_ch_schema_applied` ReplacingMergeTree table. Files must contain
// exactly one statement (ClickHouse's native protocol does not accept
// multi-statement payloads in a single Exec). The applier is "schema as
// code": every release ships with the SQL baked into the binary.
//
// Why ReplacingMergeTree for the tracking table (not MergeTree or
// ReplicatedReplacingMergeTree): ClickHouse merges happen in the
// background, which means a vanilla MergeTree could briefly show
// duplicate rows if Apply() is invoked twice in quick succession.
// ReplacingMergeTree deduplicates on the order key (here `name`) and
// keeps the newest row by `applied_at`. The cluster variant is overkill
// for v0; we add it when we move to a multi-node ClickHouse cluster in
// Phase 5.
//
// Why no transactions: ClickHouse does not support multi-statement DDL
// transactions. We rely on each individual `CREATE TABLE IF NOT EXISTS`
// being independently idempotent at the SQL level, plus the tracking
// table preventing redundant Exec calls when nothing has drifted.

// schemaFS holds the *.sql files under schema/. The applier reads them
// at Apply() time. Embedding rather than reading from disk means the
// binary ships with its schema baked in.
//
//go:embed schema
var schemaFS embed.FS

// Schema returns the embedded schema set as an fs.FS rooted at the
// schema directory. Callers (typically the control-plane main during
// boot) pass this to Apply(). Tests can pass a different fs.FS — for
// example, an fstest.MapFS — to exercise edge cases.
func Schema() fs.FS {
	sub, err := fs.Sub(schemaFS, "schema")
	if err != nil {
		// Embed paths are validated at compile time; an error here is
		// a programming bug, not a runtime condition.
		panic(fmt.Sprintf("clickhouse: embedded schema subtree missing: %v", err))
	}
	return sub
}

// trackingDDL creates the schema-tracking table if it does not exist.
// Running it twice is a no-op. Order key is `name`; ReplacingMergeTree
// dedupes on the order key, keeping the newest row by `applied_at`.
const trackingDDL = `CREATE TABLE IF NOT EXISTS _ch_schema_applied (
    name        String,
    applied_at  DateTime64(3, 'UTC') DEFAULT now64()
) ENGINE = ReplacingMergeTree(applied_at)
ORDER BY name`

// Apply runs every *.sql file in src that has not yet been recorded in
// _ch_schema_applied against the given conn, in lexicographic order.
// Files containing more than one ClickHouse statement (statements
// separated by semicolons followed by SQL tokens) will fail at Exec
// time; the convention is one statement per file.
//
// Apply is forward-only. There are no down migrations. To revert a
// change, write a new schema file that undoes it.
func Apply(ctx context.Context, conn driver.Conn, src fs.FS) error {
	if conn == nil {
		return fmt.Errorf("clickhouse: Apply called with nil conn")
	}
	if src == nil {
		return fmt.Errorf("clickhouse: Apply called with nil src")
	}

	if err := conn.Exec(ctx, trackingDDL); err != nil {
		return fmt.Errorf("clickhouse: ensure _ch_schema_applied: %w", err)
	}

	applied, err := loadAppliedSet(ctx, conn)
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
			return fmt.Errorf("clickhouse: read schema file %q: %w", name, err)
		}
		if err := applyOne(ctx, conn, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// loadAppliedSet reads the names already in _ch_schema_applied. The
// `FINAL` modifier asks ClickHouse to merge in-flight ReplacingMergeTree
// duplicates at read time, which is what we want for an authoritative
// "what has been applied" view.
func loadAppliedSet(ctx context.Context, conn driver.Conn) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx, `SELECT name FROM _ch_schema_applied FINAL`)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query _ch_schema_applied: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("clickhouse: scan _ch_schema_applied: %w", err)
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("clickhouse: iterate _ch_schema_applied: %w", err)
	}
	return applied, nil
}

// listSQLFiles returns the *.sql file names in src in lexicographic
// order. Same convention as the Postgres migration runner.
func listSQLFiles(src fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, fmt.Errorf("clickhouse: list schema dir: %w", err)
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

// applyOne runs body and records name in _ch_schema_applied. Unlike the
// Postgres runner, there is no transactional wrapper; ClickHouse does
// not support DDL transactions. Each file's SQL must be independently
// idempotent.
func applyOne(ctx context.Context, conn driver.Conn, name, body string) error {
	if err := conn.Exec(ctx, body); err != nil {
		return fmt.Errorf("clickhouse: apply %q: %w", name, err)
	}
	if err := conn.Exec(ctx,
		`INSERT INTO _ch_schema_applied(name) VALUES (?)`,
		name,
	); err != nil {
		return fmt.Errorf("clickhouse: record %q applied: %w", name, err)
	}
	return nil
}
