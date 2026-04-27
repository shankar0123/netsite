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
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEmptyDSN is returned by Open when called with an empty DSN. Tests
// and config validators rely on this sentinel rather than a string match.
var ErrEmptyDSN = errors.New("postgres: empty DSN")

// Open creates a pgx connection pool for the given DSN and verifies that
// the pool can reach the server with a Ping.
//
// The DSN follows libpq URL form, for example:
//
//	postgres://user:pass@host:5432/dbname?sslmode=require&pool_max_conns=20
//
// Pool sizing, statement caching, and other tuning happen via DSN
// parameters so operators can change behavior without code changes. See
// pgx documentation for the full parameter list.
//
// The returned pool is safe for concurrent use by multiple goroutines.
// Callers are responsible for calling pool.Close() at shutdown.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, ErrEmptyDSN
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	// Ping to fail fast on unreachable servers. Without this, the first
	// query against a misconfigured pool would surface the error far
	// from the configuration site.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return pool, nil
}
