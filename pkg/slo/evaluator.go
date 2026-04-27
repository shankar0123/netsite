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
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// What: the evaluator goroutine. On each tick (default 30s), for
// every enabled SLO:
//   1. Compute SLI over four windows: 1h, 5m, 6h, 30m.
//   2. Convert each into a burn rate.
//   3. Apply the multi-window rule:
//        fast_burn  ⇔ burn(1h)  > fastT  AND burn(5m)  > fastT
//        slow_burn  ⇔ burn(6h)  > slowT  AND burn(30m) > slowT
//   4. Compare to the previous Status. On transition into a burn
//      state, fire the Notifier.
//   5. UpsertState.
//
// How: ClickHouse handles the four window queries — `countIf` over
// `canary_results` filtered by tenant_id (+ optional test_id /
// pop_id from sli_filter). We batch the four queries per SLO into
// one round-trip via UNION ALL so each SLO is one ClickHouse
// query + one Postgres state-write per tick.
//
// Why server-side (in ClickHouse) aggregation: pulling raw rows to
// the controlplane and aggregating in Go would dwarf the SLI math
// in CPU and bandwidth. ClickHouse is built for this exact pattern.

// Reader is the narrow Store-side dependency the evaluator needs.
// Defined here (not in store.go) so unit tests can pass a fake.
type Reader interface {
	ListEnabled(ctx context.Context) ([]SLO, error)
	GetState(ctx context.Context, sloID string) (State, error)
	UpsertState(ctx context.Context, st State) error
}

// SLISource is the canary-results aggregator. The default impl
// queries ClickHouse; tests inject a synthetic series.
type SLISource interface {
	// SLI returns the success ratio (good/total, 0..1) over the
	// trailing window for the given SLO. total reflects the row
	// count over the same window — the evaluator uses it to
	// distinguish "100% success because nothing happened" (total=0)
	// from real success.
	SLI(ctx context.Context, slo SLO, window time.Duration) (sli float64, total uint64, err error)
}

// ClickHouseSLISource implements SLISource for the canary_success
// SLI kind backed by the canary_results table.
type ClickHouseSLISource struct {
	Conn driver.Conn
}

// SLI implements SLISource. Builds a parameterised ClickHouse query
// that filters by tenant + optional test_id + optional pop_id from
// slo.SLIFilter, and returns the success ratio over the window.
func (c ClickHouseSLISource) SLI(ctx context.Context, slo SLO, window time.Duration) (float64, uint64, error) {
	if slo.SLIKind != SLIKindCanarySuccess {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnsupportedSLI, slo.SLIKind)
	}
	q, args := buildCanarySLIQuery(slo, window)
	var (
		total uint64
		good  uint64
	)
	if err := c.Conn.QueryRow(ctx, q, args...).Scan(&total, &good); err != nil {
		return 0, 0, fmt.Errorf("slo: clickhouse SLI: %w", err)
	}
	if total == 0 {
		return 0, 0, nil
	}
	return float64(good) / float64(total), total, nil
}

// buildCanarySLIQuery returns a parameterised ClickHouse query that
// reports (count, count_good) over the window.
//
// We deliberately format the INTERVAL inline — ClickHouse's
// parameter substitution does not accept it as a bind value. The
// window value is computed by the evaluator from the constants
// WindowFastLong/WindowSlowLong/etc., never from user input, so the
// inline substitution is safe.
func buildCanarySLIQuery(slo SLO, window time.Duration) (string, []any) {
	args := []any{slo.TenantID}
	where := "tenant_id = ?"
	if id, ok := slo.SLIFilter["test_id"].(string); ok && id != "" {
		where += " AND test_id = ?"
		args = append(args, id)
	}
	if id, ok := slo.SLIFilter["pop_id"].(string); ok && id != "" {
		where += " AND pop_id = ?"
		args = append(args, id)
	}
	q := fmt.Sprintf(`SELECT count(), countIf(error_kind = '')
        FROM canary_results
        WHERE %s
          AND observed_at >= now() - INTERVAL %d SECOND`,
		where, int(window.Seconds()))
	return q, args
}

// Evaluator periodically re-scores every enabled SLO and dispatches
// burn events.
type Evaluator struct {
	Logger    *slog.Logger
	Reader    Reader
	SLISource SLISource
	Notifier  Notifier
	Interval  time.Duration // default 30s

	// alertCooldown bounds how often we re-fire the same burn
	// status. Without this, an SLO that stays in fast_burn for an
	// hour generates 120 alerts. Default 1 hour matches what
	// PagerDuty's incident merging does anyway, and keeps the rule
	// simple ("burn alert every hour while burning").
	alertCooldown time.Duration
}

// NewEvaluator wires defaults: 30s tick, 1h alert cooldown,
// LogNotifier when none is supplied.
func NewEvaluator(logger *slog.Logger, r Reader, s SLISource, n Notifier) *Evaluator {
	if n == nil {
		n = LogNotifier{Logger: logger}
	}
	return &Evaluator{
		Logger:        logger,
		Reader:        r,
		SLISource:     s,
		Notifier:      n,
		Interval:      30 * time.Second,
		alertCooldown: time.Hour,
	}
}

// Run blocks until ctx is canceled, ticking at e.Interval.
func (e *Evaluator) Run(ctx context.Context) error {
	if e.Interval <= 0 {
		e.Interval = 30 * time.Second
	}
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	// Fire once immediately so a freshly booted controlplane gets
	// SLO state visible without waiting an Interval.
	e.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

// tick evaluates every enabled SLO once. Errors per-SLO are logged
// but do not stop the iteration; one bad SLO should not silence the
// rest of the fleet.
func (e *Evaluator) tick(ctx context.Context) {
	slos, err := e.Reader.ListEnabled(ctx)
	if err != nil {
		e.Logger.Warn("listEnabled failed", slog.Any("err", err))
		return
	}
	now := time.Now().UTC()
	for _, s := range slos {
		if err := e.EvaluateOne(ctx, s, now); err != nil {
			e.Logger.Warn("evaluate one",
				slog.String("slo_id", s.ID), slog.Any("err", err))
		}
	}
}

// EvaluateOne is the single-SLO entry point exposed for tests.
// Computes the four-window SLI, classifies, persists state, fires
// the Notifier on a transition.
func (e *Evaluator) EvaluateOne(ctx context.Context, slo SLO, now time.Time) error {
	fastLongSLI, fastLongTotal, err := e.SLISource.SLI(ctx, slo, WindowFastLong)
	if err != nil {
		return err
	}
	fastShortSLI, fastShortTotal, err := e.SLISource.SLI(ctx, slo, WindowFastShort)
	if err != nil {
		return err
	}
	slowLongSLI, _, err := e.SLISource.SLI(ctx, slo, WindowSlowLong)
	if err != nil {
		return err
	}
	slowShortSLI, _, err := e.SLISource.SLI(ctx, slo, WindowSlowShort)
	if err != nil {
		return err
	}

	fastLongBurn := burnRate(fastLongSLI, slo.ObjectivePct)
	fastShortBurn := burnRate(fastShortSLI, slo.ObjectivePct)
	slowLongBurn := burnRate(slowLongSLI, slo.ObjectivePct)
	slowShortBurn := burnRate(slowShortSLI, slo.ObjectivePct)

	status := classify(fastLongBurn, fastShortBurn, slo.FastBurnThreshold,
		slowLongBurn, slowShortBurn, slo.SlowBurnThreshold,
		fastLongTotal+fastShortTotal == 0)

	prev, err := e.Reader.GetState(ctx, slo.ID)
	if err != nil {
		return err
	}

	newState := State{
		SLOID:           slo.ID,
		LastEvaluatedAt: now,
		LastStatus:      status,
		LastBurnRate:    fastLongBurn,
		LastAlertedAt:   prev.LastAlertedAt,
	}

	// Notify when:
	//   - we transitioned INTO a burn state, or
	//   - we are still in a burn state and last_alerted_at is older
	//     than alertCooldown.
	burning := status == StatusFastBurn || status == StatusSlowBurn
	transitioned := burning && prev.LastStatus != status
	cooldownExpired := burning &&
		!prev.LastAlertedAt.IsZero() &&
		now.Sub(prev.LastAlertedAt) >= e.alertCooldown
	freshBurn := burning && prev.LastAlertedAt.IsZero()
	if transitioned || cooldownExpired || freshBurn {
		ev := BurnEvent{
			SLOID:      slo.ID,
			SLOName:    slo.Name,
			TenantID:   slo.TenantID,
			Severity:   status,
			BurnRate:   fastLongBurn,
			Threshold:  slo.FastBurnThreshold,
			SLIValue:   fastLongSLI,
			LongWindow: WindowFastLong,
			OccurredAt: now,
		}
		if status == StatusSlowBurn {
			ev.BurnRate = slowLongBurn
			ev.Threshold = slo.SlowBurnThreshold
			ev.SLIValue = slowLongSLI
			ev.LongWindow = WindowSlowLong
		}
		notifier := e.Notifier
		// Per-SLO override: if NotifierURL is set, route through a
		// fresh WebhookNotifier rather than the default. This is
		// constructed every fire to avoid a long-lived map of
		// webhook clients keyed by URL — the alert rate is one per
		// SLO per cooldown, which is human-scale.
		if slo.NotifierURL != "" {
			notifier = NewWebhookNotifier(slo.NotifierURL)
		}
		if err := notifier.Notify(ctx, ev); err != nil {
			e.Logger.Warn("notifier failed",
				slog.String("slo_id", slo.ID), slog.Any("err", err))
		} else {
			newState.LastAlertedAt = now
		}
	} else if !burning {
		// Recovery — reset alertedAt so the next burn fires immediately.
		// We deliberately do NOT notify on recovery in v0.0.7 to keep
		// the surface area small; recovery webhooks land in Phase 1.
		newState.LastAlertedAt = time.Time{}
	}

	return e.Reader.UpsertState(ctx, newState)
}

// burnRate = (1 - SLI) / (1 - objective). Returns zero when SLI is
// zero AND objective is 1 (degenerate; avoid division-by-zero).
//
// Why we treat SLI=0 as zero burn rather than infinity: an SLI of
// zero means no good rows OR no rows at all. In the no-rows case
// there is nothing to alert on; in the all-bad case the burn rate
// is finite and large but we cap it at the divisor's reciprocal.
// The classifier uses the "burn rate exceeded threshold" logic, so
// any large finite number behaves the same as infinity.
func burnRate(sli, objective float64) float64 {
	if objective >= 1 {
		return 0
	}
	if objective <= 0 {
		return 0
	}
	return (1 - sli) / (1 - objective)
}

// classify applies the multi-window rule. fastNoData is true when
// there is no data in either fast window; we report no_data over
// "everything is fine" because operators want to know the SLO has
// gone silent rather than be reassured.
func classify(fastLongBurn, fastShortBurn, fastT, slowLongBurn, slowShortBurn, slowT float64, fastNoData bool) Status {
	if fastNoData {
		return StatusNoData
	}
	if fastLongBurn > fastT && fastShortBurn > fastT {
		return StatusFastBurn
	}
	if slowLongBurn > slowT && slowShortBurn > slowT {
		return StatusSlowBurn
	}
	return StatusOK
}

// Compile-time assertions that our concrete types satisfy the
// interfaces. Catches refactor mistakes at build time rather than
// runtime.
var (
	_ Reader    = (*Store)(nil)
	_ SLISource = ClickHouseSLISource{}
	_ Notifier  = LogNotifier{}
	_ Notifier  = (*WebhookNotifier)(nil)
)
