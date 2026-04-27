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
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shankar0123/netsite/pkg/canary"
)

// stubRunner records every Run call and returns a fixed Result.
type stubRunner struct {
	count int32
	out   canary.Result
}

func (s *stubRunner) Run(_ context.Context, _ canary.Test) canary.Result {
	atomic.AddInt32(&s.count, 1)
	return s.out
}

// stubPublisher records every Publish call.
type stubPublisher struct {
	mu   sync.Mutex
	rs   []canary.Result
	fail bool
}

func (p *stubPublisher) Publish(_ context.Context, r canary.Result) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return errors.New("intentional")
	}
	p.rs = append(p.rs, r)
	return nil
}

func (p *stubPublisher) results() []canary.Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]canary.Result, len(p.rs))
	copy(out, p.rs)
	return out
}

// stubRunners maps a single Kind to a single Runner. A Kind absent
// from the map yields ok=false.
type stubRunners map[canary.Kind]canary.Runner

func (s stubRunners) RunnerFor(k canary.Kind) (canary.Runner, bool) {
	r, ok := s[k]
	return r, ok
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestScheduler_FiresEachTestImmediately asserts that the scheduler
// kicks off one Run per Test on startup (after the jitter, which we
// neutralise by using a tiny interval and a generous wait).
func TestScheduler_FiresEachTestImmediately(t *testing.T) {
	runner := &stubRunner{out: canary.Result{TestID: "tst-1", LatencyMs: 1.0}}
	pub := &stubPublisher{}
	sch := NewScheduler(newSilentLogger(),
		stubRunners{canary.KindHTTP: runner}, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	tests := []canary.Test{
		{ID: "tst-1", Kind: canary.KindHTTP, Interval: 50 * time.Millisecond},
	}
	if err := sch.Run(ctx, tests); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expect at least one fire. Timing-sensitive tests are
	// notoriously flaky; lower-bound check is the only safe
	// assertion.
	if c := atomic.LoadInt32(&runner.count); c < 1 {
		t.Errorf("runner.count = %d; want >= 1", c)
	}
	if got := len(pub.results()); got < 1 {
		t.Errorf("publisher rows = %d; want >= 1", got)
	}
}

// TestScheduler_SkipsUnknownKind asserts a Test whose Kind has no
// registered Runner is logged and skipped, not executed.
func TestScheduler_SkipsUnknownKind(t *testing.T) {
	pub := &stubPublisher{}
	sch := NewScheduler(newSilentLogger(),
		stubRunners{}, pub) // empty registry — no Kinds resolve

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tests := []canary.Test{
		{ID: "tst-1", Kind: "weirdo", Interval: 10 * time.Millisecond},
	}
	if err := sch.Run(ctx, tests); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(pub.results()); got != 0 {
		t.Errorf("publisher rows = %d; want 0", got)
	}
}

// TestScheduler_PublishFailureIsLoggedNotFatal asserts that a publish
// error does not abort the scheduler; subsequent ticks still fire.
func TestScheduler_PublishFailureIsLoggedNotFatal(t *testing.T) {
	runner := &stubRunner{}
	pub := &stubPublisher{fail: true}
	sch := NewScheduler(newSilentLogger(),
		stubRunners{canary.KindDNS: runner}, pub)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	tests := []canary.Test{
		{ID: "tst-1", Kind: canary.KindDNS, Interval: 50 * time.Millisecond},
	}
	if err := sch.Run(ctx, tests); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := atomic.LoadInt32(&runner.count); c < 1 {
		t.Errorf("runner.count = %d; want >= 1 even with publish failures", c)
	}
}

// TestRandomJitter_InRange asserts the jitter helper falls inside the
// [0, max) range across many invocations.
func TestRandomJitter_InRange(t *testing.T) {
	max := 100 * time.Millisecond
	for i := 0; i < 1000; i++ {
		got := randomJitter(max)
		if got < 0 || got >= max {
			t.Fatalf("randomJitter(%v) = %v; outside [0, %v)", max, got, max)
		}
	}
}

// TestRandomJitter_ZeroMax returns zero immediately rather than
// panicking on `n % 0`.
func TestRandomJitter_ZeroMax(t *testing.T) {
	if got := randomJitter(0); got != 0 {
		t.Errorf("randomJitter(0) = %v; want 0", got)
	}
}
