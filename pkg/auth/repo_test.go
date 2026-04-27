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

//go:build integration

// What: integration tests for the Postgres-backed auth Repo. Boots a
// Postgres 16 container, runs the embedded migrations (which include
// 0003_auth_core.sql), and exercises the CRUD methods Service depends on.
//
// Why a separate file gated -tags integration: the unit tests in
// service_test.go already cover the business logic via an in-memory
// fake; this file is specifically about wire-up against the real
// schema. Keeping the two test layers separate matches how the rest of
// the codebase is structured (see pkg/store/postgres/migrate_test.go).

package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgstore "github.com/shankar0123/netsite/pkg/store/postgres"
)

func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("netsite_test"),
		tcpostgres.WithUsername("netsite"),
		tcpostgres.WithPassword("netsite"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(45*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pgstore.Migrate(ctx, pool, pgstore.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// TestRepo_TenantUserSession_RoundTrip walks the full happy path
// through Repo: create tenant, create user, look up for login, create
// session, fetch session, delete session.
func TestRepo_TenantUserSession_RoundTrip(t *testing.T) {
	pool := startPostgres(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := repo.EnsureTenant(ctx, Tenant{ID: "tnt-acme", Name: "Acme"}); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}

	u := User{
		ID:        "usr-abcdef0123456789",
		TenantID:  "tnt-acme",
		Email:     "User@Acme.com",
		Role:      RoleAdmin,
		CreatedAt: now,
	}
	if err := repo.CreateUser(ctx, u, "$2a$10$fakehashfakehashfakehashfakehashfakehashfakehashfakeha"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Duplicate insert returns ErrUserExists, not a generic error.
	err := repo.CreateUser(ctx, User{
		ID: "usr-otherid", TenantID: "tnt-acme", Email: "USER@acme.com", Role: RoleViewer,
	}, "$2a$10$xxx")
	if err == nil || !strings.Contains(err.Error(), "exists") {
		// errors.Is would also work but the sentinel is unexported
		// here without the alias dance; substring match is sufficient
		// because the only path producing this string is the constraint.
		if err.Error() != "auth: user already exists" {
			t.Fatalf("CreateUser dup err = %v; want ErrUserExists", err)
		}
	}

	// Lookup with a different case in the email succeeds (citext).
	got, hash, err := repo.GetUserForLogin(ctx, "tnt-acme", "user@ACME.COM")
	if err != nil {
		t.Fatalf("GetUserForLogin: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("user.ID = %q; want %q", got.ID, u.ID)
	}
	if hash == "" {
		t.Error("expected non-empty hash")
	}

	// Session round-trip.
	sess := Session{
		ID:        "ses-fedcba9876543210",
		UserID:    u.ID,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
	}
	if err := repo.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	gotSess, gotUser, err := repo.GetSession(ctx, sess.ID, now)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if gotSess.ID != sess.ID || gotUser.ID != u.ID {
		t.Errorf("session/user round-trip mismatch: got (%v, %v)", gotSess, gotUser)
	}

	// Expired session → ErrSessionNotFound.
	if _, _, err := repo.GetSession(ctx, sess.ID, now.Add(2*time.Hour)); err == nil {
		t.Error("expired session should not be returned")
	}

	if err := repo.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, _, err := repo.GetSession(ctx, sess.ID, now); err == nil {
		t.Error("deleted session should not be returned")
	}
}
