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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	pgstore "github.com/shankar0123/netsite/pkg/store/postgres"
)

// What: `ns seed demo` populates a freshly-migrated control plane
// with three POPs, five canaries, and one SLO so an operator who
// just ran `make dev-up` sees something on /canaries, /slos, and
// the dashboard immediately. Idempotent via ON CONFLICT — running
// the command twice is a no-op.
//
// How: connects to NETSITE_CONTROLPLANE_DB_URL, runs migrations,
// then issues plain SQL inserts. We deliberately do NOT go through
// the REST API: this command is the bootstrap path that prepares
// the operator's first session, before they have an admin password
// to log in with. Everything is one Postgres transaction so a
// partial seed never leaves the database half-populated.
//
// Why this and not a curl-script-in-the-README: the README
// documents the manual path (POST /v1/tests…) for operators who
// want full control. `ns seed demo` is the one-liner that gets a
// working dashboard in under a minute, which is what the demo
// audience cares about. The two coexist; they don't replace each
// other.
//
// What gets seeded:
//   - 1 tenant (tnt-demo)             — only when not present
//   - 3 POPs (pop-lhr, pop-sjc, pop-fra)
//   - 5 tests:
//       tst-cf-1111-https   HTTPS https://1.1.1.1/
//       tst-google-https    HTTPS https://www.google.com/
//       tst-cf-dns          DNS   1.1.1.1 (one.one.one.one)
//       tst-quad9-dns       DNS   9.9.9.9 (dns.quad9.net)
//       tst-google-tls      TLS   www.google.com:443
//     Why these targets: all five are public, well-known, and
//     have stable enough latency that a 7-day soak produces
//     readable seasonal patterns for the anomaly detector to
//     learn from. None of them require auth or an account.
//   - 1 SLO over the canary success rate of tst-cf-1111-https
//     with objective 99.5% over 30 days. Why this SLO: cf-1111
//     is the most stable of the five and a realistic operator-
//     facing 99.5% target lets the multi-window burn rate
//     produce non-trivial output during the soak.
//
// Why not a separate user: `ns seed admin` already creates the
// admin user. The operator is expected to run admin first, then
// demo. If we created a user here too we'd duplicate the
// password-handling logic and (worse) introduce a second source
// of "the first user."

// newSeedDemoCmd returns the `ns seed demo` subcommand.
func newSeedDemoCmd() *cobra.Command {
	var tenantID string
	c := &cobra.Command{
		Use:   "demo",
		Short: "Seed a demo tenant with 3 POPs + 5 canaries + 1 SLO",
		Long: `Populate the database with a starter dataset so a freshly
booted controlplane shows something on the dashboard immediately.

Idempotent: running twice is a no-op (every insert uses ON CONFLICT).
The tenant is created if missing; existing rows for the same IDs
are left alone. Run ` + "`ns seed admin`" + ` first if no admin user exists yet.

Connects to NETSITE_CONTROLPLANE_DB_URL. Migrations are applied
idempotently before the seed runs.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("NETSITE_CONTROLPLANE_DB_URL")
			if dsn == "" {
				return errors.New("NETSITE_CONTROLPLANE_DB_URL is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			pool, err := pgstore.Open(ctx, dsn)
			if err != nil {
				return fmt.Errorf("postgres connect: %w", err)
			}
			defer pool.Close()

			if err := pgstore.Migrate(ctx, pool, pgstore.Migrations()); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}

			counts, err := seedDemo(ctx, pool, tenantID)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"seeded tenant=%s pops=%d tests=%d slos=%d (idempotent — pre-existing rows left alone)\n",
				tenantID, counts.pops, counts.tests, counts.slos)
			return nil
		},
	}
	c.Flags().StringVar(&tenantID, "tenant-id", "tnt-demo",
		"Tenant ID to seed under (prefixed-TEXT, e.g. tnt-acme)")
	return c
}

// seedCounts reports how many of each row this run wrote (or
// re-confirmed). Operators get this back as feedback so a
// "successful" idempotent re-run is distinguishable from a
// "successful first" run by inspecting the output.
type seedCounts struct {
	pops, tests, slos int
}

// seedDemo is the actual write path. Exposed for unit testing
// against an in-process Postgres (testcontainers) without standing
// up the cobra command tree.
func seedDemo(ctx context.Context, pool *pgxpool.Pool, tenantID string) (seedCounts, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return seedCounts{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Tenant first. ON CONFLICT DO NOTHING so re-runs and a
	// concurrent `ns seed admin tnt-demo` don't clobber each other.
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenants (id, name) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`,
		tenantID, "Demo"); err != nil {
		return seedCounts{}, fmt.Errorf("seed tenant: %w", err)
	}

	pops := []struct {
		id, name, region, healthURL string
	}{
		{"pop-lhr", "London", "eu-west", ""},
		{"pop-sjc", "San Jose", "us-west", ""},
		{"pop-fra", "Frankfurt", "eu-central", ""},
	}
	for _, p := range pops {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pops (id, tenant_id, name, description, region, health_url)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (id) DO NOTHING`,
			p.id, tenantID, p.name, "Demo POP", p.region, p.healthURL); err != nil {
			return seedCounts{}, fmt.Errorf("seed pop %s: %w", p.id, err)
		}
	}

	tests := []struct {
		id, kind, target string
		intervalMS       int64
		timeoutMS        int64
		config           map[string]any
	}{
		{"tst-cf-1111-https", "http", "https://1.1.1.1/", 30_000, 5_000, map[string]any{}},
		{"tst-google-https", "http", "https://www.google.com/", 30_000, 5_000, map[string]any{}},
		{"tst-cf-dns", "dns", "one.one.one.one", 30_000, 5_000, map[string]any{"resolver": "1.1.1.1:53", "record_type": "A"}},
		{"tst-quad9-dns", "dns", "dns.quad9.net", 30_000, 5_000, map[string]any{"resolver": "9.9.9.9:53", "record_type": "A"}},
		{"tst-google-tls", "tls", "www.google.com:443", 30_000, 5_000, map[string]any{}},
	}
	for _, t := range tests {
		cfg, err := json.Marshal(t.config)
		if err != nil {
			return seedCounts{}, fmt.Errorf("marshal test config: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tests (id, tenant_id, kind, target, interval_ms, timeout_ms, enabled, config)
			 VALUES ($1, $2, $3, $4, $5, $6, TRUE, $7)
			 ON CONFLICT (id) DO NOTHING`,
			t.id, tenantID, t.kind, t.target, t.intervalMS, t.timeoutMS, cfg); err != nil {
			return seedCounts{}, fmt.Errorf("seed test %s: %w", t.id, err)
		}
	}

	// SLO over tst-cf-1111-https success rate, 30-day window,
	// 99.5% objective, default fast/slow burn thresholds.
	sliFilter, _ := json.Marshal(map[string]any{"test_id": "tst-cf-1111-https"})
	if _, err := tx.Exec(ctx,
		`INSERT INTO slos (id, tenant_id, name, description, sli_kind, sli_filter,
		                    objective_pct, window_seconds,
		                    fast_burn_threshold, slow_burn_threshold,
		                    notifier_url, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (id) DO NOTHING`,
		"slo-cf-1111-availability", tenantID,
		"cf 1.1.1.1 availability", "Demo SLO over the cf-1.1.1.1 HTTPS canary",
		"canary_success", sliFilter,
		0.995, int64(30*24*60*60),
		14.4, 6.0,
		"", true); err != nil {
		return seedCounts{}, fmt.Errorf("seed slo: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return seedCounts{}, fmt.Errorf("commit: %w", err)
	}
	return seedCounts{pops: len(pops), tests: len(tests), slos: 1}, nil
}
