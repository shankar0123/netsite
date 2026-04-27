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

package popagent

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// What: a per-Test goroutine driver. For each Test, the scheduler
// computes a startup jitter, sleeps for it, then runs the Test on a
// `time.Ticker` at Test.Interval. Each tick calls into a Runner
// chosen by Test.Kind and forwards the Result to a Publisher.
//
// How: Run() blocks until ctx is canceled, then waits for all per-
// Test goroutines to drain. The jitter prevents a thundering-herd
// problem: a freshly booted POP would otherwise fire every Test
// simultaneously, producing burst traffic to every target.
//
// Why a separate scheduler type rather than spawning goroutines
// directly inside main: the scheduler is the right unit to test —
// pass it a fake Runner and a fake Publisher and assert that Run
// fires the right Tests on the right cadence. main is small and
// orchestration-only.

// Runners selects a canary.Runner by Test.Kind. The POP main wires
// in DNS, HTTP, and TLS runners; tests wire in stubs.
type Runners interface {
	RunnerFor(kind canary.Kind) (canary.Runner, bool)
}

// Publisher sinks a Result somewhere durable (NATS in production,
// a buffered slice in tests).
type Publisher interface {
	Publish(ctx context.Context, r canary.Result) error
}

// Scheduler runs Tests on their configured intervals.
type Scheduler struct {
	logger    *slog.Logger
	runners   Runners
	publisher Publisher
}

// NewScheduler constructs a Scheduler with the given dependencies.
// All three are required.
func NewScheduler(logger *slog.Logger, runners Runners, publisher Publisher) *Scheduler {
	return &Scheduler{logger: logger, runners: runners, publisher: publisher}
}

// Run starts a per-Test goroutine for each test in tests and blocks
// until ctx is canceled. Returns nil on clean shutdown.
//
// Errors during Test execution are logged but do not abort the
// scheduler — a Test that consistently fails is still data, and a
// rare publish failure should not take down the agent.
func (s *Scheduler) Run(ctx context.Context, tests []canary.Test) error {
	if s.logger == nil || s.runners == nil || s.publisher == nil {
		return fmt.Errorf("popagent: nil scheduler dependency")
	}
	var wg sync.WaitGroup
	for _, t := range tests {
		t := t
		runner, ok := s.runners.RunnerFor(t.Kind)
		if !ok {
			s.logger.Warn("no runner for kind; skipping test",
				slog.String("test_id", t.ID), slog.String("kind", string(t.Kind)))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runOne(ctx, t, runner)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// runOne drives a single Test on its interval. Jitter is uniform in
// [0, interval) so a freshly started fleet of POPs spreads its load
// across the full interval window rather than aligning at second 0.
func (s *Scheduler) runOne(ctx context.Context, t canary.Test, runner canary.Runner) {
	jitter := randomJitter(t.Interval)
	select {
	case <-time.After(jitter):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	// Fire once immediately after the jitter — the operator wants
	// data within ~Interval of POP boot, not ~2*Interval.
	s.fire(ctx, t, runner)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fire(ctx, t, runner)
		}
	}
}

// fire executes one tick: run the Runner, ship the Result.
func (s *Scheduler) fire(ctx context.Context, t canary.Test, runner canary.Runner) {
	result := runner.Run(ctx, t)
	if err := s.publisher.Publish(ctx, result); err != nil {
		s.logger.Warn("publish failed",
			slog.String("test_id", t.ID),
			slog.Any("err", err),
		)
	}
}

// randomJitter returns a uniformly random duration in [0, max). Uses
// crypto/rand because (a) we already depend on it via pkg/auth and
// (b) avoiding math/rand sidesteps the global-PRNG seeding question
// without buying into a per-call init.
//
// On the rare RNG failure, fall back to no jitter; the consequence is
// thundering-herd which is bad but not fatal.
func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	n := binary.BigEndian.Uint64(b[:])
	return time.Duration(n % uint64(max))
}
