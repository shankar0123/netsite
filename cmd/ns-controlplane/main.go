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

// Command ns-controlplane is the NetSite control-plane HTTP server.
//
// What: a single static binary that exposes the v1 REST API. It owns
// the Postgres connection (and runs migrations on boot), the Prometheus
// registry that backs /metrics, and the OpenTelemetry trace + metric
// pipelines. POPs and operator CLIs talk to this server.
//
// How: main() defers to run() (testable, returns an exit code; main
// itself is a one-liner). run() reads env config, sets up OTel,
// connects to Postgres, applies migrations, builds the API server,
// and blocks until SIGINT/SIGTERM. Graceful shutdown drains in-flight
// requests for up to 30s before exiting.
//
// Why a thin main: testing main() is impossible (it calls os.Exit).
// Putting the boot logic in run() keeps it unit-testable for failure
// paths (missing DSN, OTel-setup failure, migration failure) which
// matters because operators routinely diagnose deploys by reading
// startup logs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shankar0123/netsite/pkg/api"
	"github.com/shankar0123/netsite/pkg/auth"
	"github.com/shankar0123/netsite/pkg/canary/ingest"
	"github.com/shankar0123/netsite/pkg/integrations/otel"
	chstore "github.com/shankar0123/netsite/pkg/store/clickhouse"
	natsstore "github.com/shankar0123/netsite/pkg/store/nats"
	pgstore "github.com/shankar0123/netsite/pkg/store/postgres"
	promstore "github.com/shankar0123/netsite/pkg/store/prometheus"
	"github.com/shankar0123/netsite/pkg/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// Exit codes are deliberately distinct so operators reading logs can
// tell which boot-phase failed without grepping. Treat these as a
// stable contract: do not renumber.
const (
	exitOK             = 0
	exitOTelSetup      = 2
	exitMissingDSN     = 3
	exitDBConnect      = 4
	exitMigrate        = 5
	exitServerBuild    = 6
	exitServerRuntime  = 7
	exitNATSConnect    = 8
	exitJetStream      = 9
	exitEnsureStream   = 10
	exitClickHouseConn = 11
)

func run(_ []string) int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("ns-controlplane booting", slog.String("version", version.String()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// OTel must come up before everything else so subsequent boot
	// steps (Postgres connect, migration apply) emit traceable spans.
	otelCfg := otel.ConfigFromEnv("ns-controlplane", version.Version)
	otelShutdown, err := otel.Setup(ctx, otelCfg)
	if err != nil {
		logger.Error("otel setup failed", slog.Any("err", err))
		return exitOTelSetup
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(sctx); err != nil {
			logger.Error("otel shutdown error", slog.Any("err", err))
		}
	}()

	dsn := os.Getenv("NETSITE_CONTROLPLANE_DB_URL")
	if dsn == "" {
		logger.Error("NETSITE_CONTROLPLANE_DB_URL is required")
		return exitMissingDSN
	}

	pool, err := pgstore.Open(ctx, dsn)
	if err != nil {
		logger.Error("postgres connect failed", slog.Any("err", err))
		return exitDBConnect
	}
	defer pool.Close()

	logger.Info("running embedded migrations")
	if err := pgstore.Migrate(ctx, pool, pgstore.Migrations()); err != nil {
		logger.Error("migrate failed", slog.Any("err", err))
		return exitMigrate
	}

	promReg := promstore.NewRegistry()

	// NATS + JetStream + canary-results stream + ingest consumer.
	// The consumer ingests POP-published results into ClickHouse on a
	// goroutine in parallel with the HTTP server.
	natsURL := envOr("NETSITE_CONTROLPLANE_NATS_URL", "nats://localhost:4222")
	nc, err := natsstore.Connect(natsURL, "ns-controlplane")
	if err != nil {
		logger.Error("nats connect failed", slog.Any("err", err))
		return exitNATSConnect
	}
	defer nc.Close()

	js, err := natsstore.JetStream(nc)
	if err != nil {
		logger.Error("jetstream init failed", slog.Any("err", err))
		return exitJetStream
	}
	if err := ingest.EnsureStream(js); err != nil {
		logger.Error("ensure canary-results stream failed", slog.Any("err", err))
		return exitEnsureStream
	}

	// ClickHouse for canary_results ingestion. The schema applier
	// reuses pkg/store/clickhouse.Apply so a fresh deploy gets all
	// embedded tables before the consumer starts writing.
	chURL := envOr("NETSITE_CONTROLPLANE_CH_URL", "")
	if chURL == "" {
		logger.Error("NETSITE_CONTROLPLANE_CH_URL is required")
		return exitClickHouseConn
	}
	chConn, err := chstore.Open(ctx, chURL)
	if err != nil {
		logger.Error("clickhouse connect failed", slog.Any("err", err))
		return exitClickHouseConn
	}
	defer func() { _ = chConn.Close() }()
	if err := chstore.Apply(ctx, chConn, chstore.Schema()); err != nil {
		logger.Error("clickhouse schema apply failed", slog.Any("err", err))
		return exitClickHouseConn
	}

	consumer := ingest.NewConsumer(logger, js, chConn)
	go func() {
		if err := consumer.Run(ctx); err != nil {
			logger.Error("ingest consumer stopped with error", slog.Any("err", err))
		}
	}()

	authSvc := auth.NewService(auth.NewRepo(pool), auth.Config{})

	addr := envOr("NETSITE_CONTROLPLANE_HTTP_ADDR", ":8080")
	srv, err := api.New(api.Config{
		Addr:    addr,
		Pool:    pool,
		Logger:  logger,
		PromReg: promReg,
		Auth:    authSvc,
	})
	if err != nil {
		logger.Error("api server build failed", slog.Any("err", err))
		return exitServerBuild
	}

	logger.Info("serving", slog.String("addr", addr))
	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped with error", slog.Any("err", err))
		return exitServerRuntime
	}
	logger.Info("ns-controlplane shutdown complete")
	return exitOK
}

// envOr returns the value of key if set and non-empty, otherwise def.
// Mirrors the empty-as-default behavior used by pkg/integrations/otel.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
