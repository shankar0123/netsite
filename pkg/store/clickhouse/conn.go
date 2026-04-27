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
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrEmptyDSN is returned by Open when called with an empty DSN.
var ErrEmptyDSN = errors.New("clickhouse: empty DSN")

// Open creates a ClickHouse connection (using the native protocol) for
// the given DSN, and verifies the server is reachable with a Ping.
//
// The DSN follows clickhouse-go/v2 URL form:
//
//	clickhouse://user:pass@host:9000/dbname?dial_timeout=10s
//
// Tuning parameters (max connections, compression, secure TLS) are
// expressed as DSN query parameters so operators can adjust without
// code changes. See the clickhouse-go/v2 docs for the full list.
//
// The returned driver.Conn is safe for concurrent use. Callers are
// responsible for calling Close() at shutdown.
func Open(ctx context.Context, dsn string) (driver.Conn, error) {
	if dsn == "" {
		return nil, ErrEmptyDSN
	}

	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: parse DSN: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open conn: %w", err)
	}

	// Ping fails fast on misconfigured DSNs and unreachable servers.
	// Without this, the first SELECT/INSERT against a broken conn
	// would surface the error far from the configuration site.
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping: %w", err)
	}

	return conn, nil
}
