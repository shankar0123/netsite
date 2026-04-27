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

package slo

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"
)

// fakeReader is an in-memory Reader for tests.
type fakeReader struct {
	mu     sync.Mutex
	slos   []SLO
	states map[string]State
}

func newFakeReader(s ...SLO) *fakeReader {
	return &fakeReader{slos: s, states: map[string]State{}}
}

func (f *fakeReader) ListEnabled(_ context.Context) ([]SLO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SLO, 0, len(f.slos))
	for _, s := range f.slos {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeReader) GetState(_ context.Context, id string) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.states[id]; ok {
		return s, nil
	}
	return State{SLOID: id, LastStatus: StatusUnknown}, nil
}

func (f *fakeReader) UpsertState(_ context.Context, st State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[st.SLOID] = st
	return nil
}

// fakeSLI lets the test prescribe per-window SLI values per SLO.
type fakeSLI struct {
	values map[time.Duration]struct {
		sli   float64
		total uint64
	}
}

func (f fakeSLI) SLI(_ context.Context, _ SLO, w time.Duration) (float64, uint64, error) {
	v, ok := f.values[w]
	if !ok {
		return 1.0, 0, nil
	}
	return v.sli, v.total, nil
}

// captureNotifier records every BurnEvent it sees.
type captureNotifier struct {
	mu sync.Mutex
	rs []BurnEvent
}

func (c *captureNotifier) Notify(_ context.Context, ev BurnEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rs = append(c.rs, ev)
	return nil
}
func (c *captureNotifier) events() []BurnEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]BurnEvent, len(c.rs))
	copy(out, c.rs)
	return out
}

func newQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sampleSLO() SLO {
	return SLO{
		ID: "slo-1", TenantID: "tnt-x", Name: "test",
		SLIKind:           SLIKindCanarySuccess,
		ObjectivePct:      0.999,
		WindowSeconds:     30 * 24 * 3600,
		FastBurnThreshold: 14.4,
		SlowBurnThreshold: 6.0,
		Enabled:           true,
	}
}

// TestBurnRate_Math walks the closed-form burn rate equation across
// representative SLI/objective pairs.
func TestBurnRate_Math(t *testing.T) {
	cases := []struct {
		name string
		sli  float64
		obj  float64
		want float64
	}{
		{"perfect SLI no burn", 1.0, 0.999, 0.0},
		{"at objective", 0.999, 0.999, 1.0},
		{"10x burn", 0.99, 0.999, 10.0},
		{"100x burn", 0.9, 0.999, 100.0},
		{"degenerate obj=1", 0.5, 1.0, 0.0},
		{"degenerate obj=0", 0.5, 0.0, 0.0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := burnRate(tc.sli, tc.obj)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("burnRate(%v, %v) = %v; want %v", tc.sli, tc.obj, got, tc.want)
			}
		})
	}
}

// TestClassify_Matrix exercises every cell of the multi-window
// classifier.
func TestClassify_Matrix(t *testing.T) {
	cases := []struct {
		name                                     string
		fastLong, fastShort, slowLong, slowShort float64
		fastT, slowT                             float64
		noData                                   bool
		want                                     Status
	}{
		{"no data", 0, 0, 0, 0, 14.4, 6.0, true, StatusNoData},
		{"all clean", 0, 0, 0, 0, 14.4, 6.0, false, StatusOK},
		{"fast long over but short under", 20, 5, 0, 0, 14.4, 6.0, false, StatusOK},
		{"fast both over", 20, 20, 0, 0, 14.4, 6.0, false, StatusFastBurn},
		{"slow only", 0, 0, 7, 7, 14.4, 6.0, false, StatusSlowBurn},
		{"both over fast wins", 20, 20, 7, 7, 14.4, 6.0, false, StatusFastBurn},
		{"slow long over short under", 0, 0, 7, 5, 14.4, 6.0, false, StatusOK},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.fastLong, tc.fastShort, tc.fastT,
				tc.slowLong, tc.slowShort, tc.slowT, tc.noData)
			if got != tc.want {
				t.Errorf("got %s; want %s", got, tc.want)
			}
		})
	}
}

// TestEvaluator_FastBurn_FiresOnce asserts that an SLO whose four
// windows are all in fast-burn fires the notifier exactly once on
// the first transition (until the cooldown elapses).
func TestEvaluator_FastBurn_FiresOnce(t *testing.T) {
	r := newFakeReader(sampleSLO())
	source := fakeSLI{values: map[time.Duration]struct {
		sli   float64
		total uint64
	}{
		WindowFastLong:  {0.90, 1000},
		WindowFastShort: {0.85, 100},
		WindowSlowLong:  {0.998, 6000},
		WindowSlowShort: {0.998, 500},
	}}
	notif := &captureNotifier{}
	e := NewEvaluator(newQuietLogger(), r, source, notif)
	e.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.EvaluateOne(ctx, r.slos[0], time.Now().UTC()); err != nil {
		t.Fatalf("EvaluateOne: %v", err)
	}
	if got := len(notif.events()); got != 1 {
		t.Fatalf("first eval fires = %d; want 1", got)
	}

	// Second eval inside cooldown should not re-fire.
	if err := e.EvaluateOne(ctx, r.slos[0], time.Now().UTC()); err != nil {
		t.Fatalf("EvaluateOne: %v", err)
	}
	if got := len(notif.events()); got != 1 {
		t.Errorf("second eval inside cooldown fires = %d; want 1", got)
	}
}

// TestEvaluator_RecoveryClearsCooldown asserts that going from
// burning back to ok wipes LastAlertedAt so a subsequent burn fires
// immediately even though the absolute time gap may be < cooldown.
func TestEvaluator_RecoveryClearsCooldown(t *testing.T) {
	slo := sampleSLO()
	r := newFakeReader(slo)
	burningSrc := fakeSLI{values: map[time.Duration]struct {
		sli   float64
		total uint64
	}{
		WindowFastLong:  {0.90, 1000},
		WindowFastShort: {0.85, 100},
		WindowSlowLong:  {0.998, 6000},
		WindowSlowShort: {0.998, 500},
	}}
	cleanSrc := fakeSLI{values: map[time.Duration]struct {
		sli   float64
		total uint64
	}{
		WindowFastLong:  {0.9999, 1000},
		WindowFastShort: {0.9999, 100},
		WindowSlowLong:  {0.9999, 6000},
		WindowSlowShort: {0.9999, 500},
	}}
	notif := &captureNotifier{}
	now := time.Now().UTC()

	e := NewEvaluator(newQuietLogger(), r, burningSrc, notif)
	if err := e.EvaluateOne(context.Background(), slo, now); err != nil {
		t.Fatal(err)
	}
	if got := len(notif.events()); got != 1 {
		t.Fatalf("burn fire = %d; want 1", got)
	}

	// Recovery.
	e.SLISource = cleanSrc
	if err := e.EvaluateOne(context.Background(), slo, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _ := r.GetState(context.Background(), slo.ID)
	if st.LastStatus != StatusOK {
		t.Errorf("post-recovery status = %s; want ok", st.LastStatus)
	}
	if !st.LastAlertedAt.IsZero() {
		t.Errorf("post-recovery LastAlertedAt = %v; want zero", st.LastAlertedAt)
	}

	// New burn → fires immediately even though wall clock < cooldown.
	e.SLISource = burningSrc
	if err := e.EvaluateOne(context.Background(), slo, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := len(notif.events()); got != 2 {
		t.Errorf("post-recovery burn fire = %d; want 2", got)
	}
}

// TestBuildCanarySLIQuery_FilterShape asserts the optional filters
// land in the WHERE clause when present and not when absent.
func TestBuildCanarySLIQuery_FilterShape(t *testing.T) {
	t.Run("tenant only", func(t *testing.T) {
		q, args := buildCanarySLIQuery(SLO{TenantID: "tnt-x", SLIKind: SLIKindCanarySuccess}, time.Hour)
		if got := len(args); got != 1 {
			t.Errorf("args = %d; want 1", got)
		}
		if !contains(q, "tenant_id = ?") || contains(q, "test_id") || contains(q, "pop_id") {
			t.Errorf("unexpected clause: %s", q)
		}
	})
	t.Run("test_id filter", func(t *testing.T) {
		q, args := buildCanarySLIQuery(SLO{
			TenantID:  "tnt-x",
			SLIKind:   SLIKindCanarySuccess,
			SLIFilter: map[string]any{"test_id": "tst-1"},
		}, time.Hour)
		if got := len(args); got != 2 {
			t.Errorf("args = %d; want 2", got)
		}
		if !contains(q, "test_id = ?") {
			t.Errorf("missing test_id clause: %s", q)
		}
	})
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
