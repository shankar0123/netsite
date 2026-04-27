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

// Command ns-pop is the NetSite POP-side agent. It runs canaries on a
// schedule defined by a YAML config file and publishes Results to
// NATS JetStream for the control plane to ingest.
//
// What: a single static binary suitable for shipping to every POP.
// At boot, it loads its config, dials NATS, builds runners for each
// supported canary protocol, and hands the test list to the
// scheduler. Graceful shutdown drains in-flight ticks before exit.
//
// How: main → run() → load config → set up OTel → connect NATS →
// build runners + publisher + scheduler → block on signal. The same
// thin-main / fat-run pattern as ns-controlplane keeps boot logic
// unit-testable.
//
// Why one binary across DNS/HTTP/TLS/(later)ICMP rather than one per
// protocol: POPs run a small number of tests per protocol; deploying
// four binaries to each POP would 4× the operational surface for no
// benefit. When ICMP raw-socket capability is required, POPs that
// cannot grant it will simply not configure ICMP tests; the binary
// remains the same.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
	"github.com/shankar0123/netsite/pkg/integrations/otel"
	"github.com/shankar0123/netsite/pkg/popagent"
	natsstore "github.com/shankar0123/netsite/pkg/store/nats"
	"github.com/shankar0123/netsite/pkg/version"
)

// Boot-phase exit codes. Stable contract; do not renumber.
const (
	exitOK            = 0
	exitOTelSetup     = 2
	exitMissingConfig = 3
	exitConfigLoad    = 4
	exitNATSConnect   = 5
	exitJetStream     = 6
	exitSchedulerStop = 7
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(_ []string) int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("ns-pop booting", slog.String("version", version.String()))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	otelCfg := otel.ConfigFromEnv("ns-pop", version.Version)
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

	configPath := os.Getenv("NETSITE_POP_CONFIG")
	if configPath == "" {
		logger.Error("NETSITE_POP_CONFIG is required")
		return exitMissingConfig
	}

	cfg, err := popagent.LoadConfig(configPath)
	if err != nil {
		logger.Error("config load failed", slog.Any("err", err))
		return exitConfigLoad
	}
	logger.Info("config loaded",
		slog.String("pop_id", cfg.PopID),
		slog.Int("tests", len(cfg.Tests)),
	)

	nc, err := natsstore.Connect(cfg.NATSURL, "ns-pop:"+cfg.PopID)
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

	publisher := popagent.NewNATSPublisher(js)
	runners := popagent.DefaultRunners(cfg.PopID)

	tests := make([]canary.Test, 0, len(cfg.Tests))
	for _, td := range cfg.Tests {
		t, err := td.ToCanaryTest()
		if err != nil {
			logger.Warn("skipping bad test", slog.String("id", td.ID), slog.Any("err", err))
			continue
		}
		tests = append(tests, t)
	}

	// Append mesh-generated tests, if any. Skip self automatically.
	mesh, err := popagent.GenerateMeshTests(cfg.Mesh, cfg.PopID)
	if err != nil {
		logger.Warn("mesh generation failed", slog.Any("err", err))
	}
	tests = append(tests, mesh...)
	if len(mesh) > 0 {
		logger.Info("mesh tests generated", slog.Int("count", len(mesh)))
	}

	scheduler := popagent.NewScheduler(logger, runners, publisher)
	logger.Info("scheduler running", slog.Int("tests", len(tests)))
	if err := scheduler.Run(ctx, tests); err != nil {
		logger.Error("scheduler stopped with error", slog.Any("err", err))
		return exitSchedulerStop
	}
	logger.Info("ns-pop shutdown complete")
	return exitOK
}
