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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shankar0123/netsite/pkg/auth"
	pgstore "github.com/shankar0123/netsite/pkg/store/postgres"
)

// What: a CLI subcommand `ns seed admin` that bootstraps a tenant and
// the first admin user on a fresh database.
//
// How: connects to the Postgres pointed at by NETSITE_CONTROLPLANE_DB_URL,
// runs migrations idempotently, then calls auth.Repo + auth.Service to
// create the tenant and admin. All inputs are flags; the password may
// also be provided via NETSITE_SEED_PASSWORD so it never appears in
// shell history.
//
// Why a separate `seed` subcommand instead of an HTTP endpoint: the
// first admin is a chicken-and-egg situation — you cannot use the
// auth-protected API to create the user the API will require. CLI is
// the right shape: an operator with database access bootstraps once.

// newSeedCmd returns the parent `seed` command. Subcommands are added
// alongside it (only `admin` exists today).
func newSeedCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "seed",
		Short: "Bootstrap commands run against the database directly",
		Long: `Subcommands here connect to NETSITE_CONTROLPLANE_DB_URL,
apply migrations, and seed minimal records that other commands will
later reference. Intended to be run once per cluster by an operator
with database access.`,
	}
	c.AddCommand(newSeedAdminCmd())
	return c
}

// newSeedAdminCmd returns `ns seed admin` — creates a tenant and the
// initial admin user in a single transaction.
func newSeedAdminCmd() *cobra.Command {
	var (
		tenantID   string
		tenantName string
		email      string
		password   string
	)
	c := &cobra.Command{
		Use:   "admin",
		Short: "Create the first tenant and admin user",
		Long: `Create the first tenant and admin user.

Required flags:
  --email       admin email
Either of:
  --password    admin password (visible in shell history)
  NETSITE_SEED_PASSWORD environment variable (preferred)

Optional flags:
  --tenant-id   default "tnt-default"
  --tenant-name default "Default Tenant"

Connects to NETSITE_CONTROLPLANE_DB_URL. Migrations are applied
idempotently before the seed runs.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if password == "" {
				password = os.Getenv("NETSITE_SEED_PASSWORD")
			}
			if email == "" {
				return errors.New("--email is required")
			}
			if password == "" {
				return errors.New("--password or NETSITE_SEED_PASSWORD is required")
			}
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

			repo := auth.NewRepo(pool)
			if err := repo.EnsureTenant(ctx, auth.Tenant{ID: tenantID, Name: tenantName}); err != nil {
				return err
			}

			svc := auth.NewService(repo, auth.Config{})
			user, err := svc.CreateUser(ctx, auth.CreateUserInput{
				TenantID: tenantID,
				Email:    strings.TrimSpace(email),
				Password: password,
				Role:     auth.RoleAdmin,
			})
			if err != nil {
				if errors.Is(err, auth.ErrUserExists) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(),
						"admin already exists for tenant=%s email=%s — nothing to do\n",
						tenantID, email)
					return nil
				}
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"created tenant=%s admin=%s id=%s\n",
				tenantID, user.Email, user.ID)
			return nil
		},
	}
	c.Flags().StringVar(&tenantID, "tenant-id", "tnt-default", "Tenant ID (prefixed-TEXT, e.g. tnt-acme)")
	c.Flags().StringVar(&tenantName, "tenant-name", "Default Tenant", "Human-readable tenant display name")
	c.Flags().StringVar(&email, "email", "", "Admin email (required)")
	c.Flags().StringVar(&password, "password", "", "Admin password; prefer NETSITE_SEED_PASSWORD env")
	return c
}
