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

// Package postgres is the Postgres data-access layer for the NetSite
// control plane.
//
// What: this package owns the pgx connection pool, the schema migration
// runner, and (in later tasks) per-table repositories.
//
// How: pgxpool.Pool is the long-lived handle. Open() parses a DSN and
// returns a configured pool. Migrate() applies SQL files from an embed.FS
// in lexicographic order, tracking applied names in _schema_migrations.
//
// Why pgx (over database/sql with lib/pq): pgx is the de-facto Go driver
// for Postgres, has first-class support for Postgres-only features (LISTEN/
// NOTIFY, COPY, JSON path types) that we'll need by Phase 3, and is
// actively maintained. database/sql adds an interface layer NetSite does
// not need, since we are committed to Postgres for relational data per
// architecture invariant A2 in CLAUDE.md.
//
// Why an explicit Open(ctx, dsn) instead of magic env-var loading: keeps
// configuration assembly out of this package. Callers (cmd/ns-controlplane
// in Task 0.12) read NETSITE_CONTROLPLANE_DB_URL from the environment and
// pass it in, which keeps tests trivial — they construct a DSN from a
// testcontainers Postgres and pass it directly without environment
// pollution.
package postgres
