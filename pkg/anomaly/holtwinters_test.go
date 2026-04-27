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

package anomaly

import (
	"errors"
	"math"
	"testing"
)

// floatNear is the standard tolerance helper. The Holt-Winters
// recursion accumulates floating-point error proportionally to series
// length; 1e-9 is too tight, 1e-3 is too loose for unit tests.
func floatNear(a, b, eps float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// pureSeasonalNoTrend builds a no-trend, no-noise additive series:
//
//	y_i = base + amp * sin(2π * (i % period) / period)
//
// We use this rather than seasonalSeries (which adds noise) because
// the analytical expectations below assume zero residual after the
// seed window.
func pureSeasonalNoTrend(n, period int, base, amp float64) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		theta := 2 * math.Pi * float64(i%period) / float64(period)
		out[i] = base + amp*math.Sin(theta)
	}
	return out
}

// TestHoltWinters_InvalidPeriod asserts the period guard.
func TestHoltWinters_InvalidPeriod(t *testing.T) {
	_, err := HoltWinters([]float64{1, 2, 3, 4}, 1, 0.3, 0.1, 0.1)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("err = %v; want ErrInvalidPeriod", err)
	}
	_, err = HoltWinters([]float64{1, 2, 3, 4}, 0, 0.3, 0.1, 0.1)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("err = %v; want ErrInvalidPeriod", err)
	}
}

// TestHoltWinters_InsufficientData asserts the >= 2*period guard.
func TestHoltWinters_InsufficientData(t *testing.T) {
	// period 4, n=7 < 8.
	_, err := HoltWinters([]float64{1, 2, 3, 4, 5, 6, 7}, 4, 0.3, 0.1, 0.1)
	if !errors.Is(err, ErrInsufficientData) {
		t.Errorf("err = %v; want ErrInsufficientData", err)
	}
}

// TestHoltWinters_PureSeasonalLowError asserts that on a noiseless
// pure-seasonal series, the in-sample fit converges to the truth and
// residuals stay small.
func TestHoltWinters_PureSeasonalLowError(t *testing.T) {
	period := 24
	n := period * 4 // 4 cycles
	ys := pureSeasonalNoTrend(n, period, 1.0, 0.05)
	fit, err := HoltWinters(ys, period, 0.3, 0.1, 0.1)
	if err != nil {
		t.Fatalf("HoltWinters: %v", err)
	}
	if len(fit.InSample) != n {
		t.Fatalf("len(InSample) = %d; want %d", len(fit.InSample), n)
	}
	if len(fit.Seasonals) != period {
		t.Fatalf("len(Seasonals) = %d; want %d", len(fit.Seasonals), period)
	}

	// After the seed (first period) the model should track within a
	// modest tolerance — no noise, only its own initialisation error
	// carrying forward through the recursion.
	resid := fit.Residuals(ys)
	var maxAbs float64
	for i := period; i < n; i++ {
		a := resid[i]
		if a < 0 {
			a = -a
		}
		if a > maxAbs {
			maxAbs = a
		}
	}
	// Empirically with α=β=γ=(0.3,0.1,0.1) and 4 cycles the fit is
	// within 0.02 of truth at every point. Anchor at 0.05 to leave
	// headroom for portability across math libraries.
	if maxAbs > 0.05 {
		t.Errorf("max abs residual = %.4f; want <= 0.05", maxAbs)
	}
}

// TestHoltWinters_ResidualsLength asserts Residuals returns one entry
// per input.
func TestHoltWinters_ResidualsLength(t *testing.T) {
	period := 12
	ys := pureSeasonalNoTrend(period*3, period, 1, 0.1)
	fit, err := HoltWinters(ys, period, 0.3, 0.1, 0.1)
	if err != nil {
		t.Fatalf("HoltWinters: %v", err)
	}
	r := fit.Residuals(ys)
	if len(r) != len(ys) {
		t.Errorf("len(Residuals) = %d; want %d", len(r), len(ys))
	}
	// The first `period` entries are zero by construction (seed
	// window mirrors input).
	for i := 0; i < period; i++ {
		if r[i] != 0 {
			t.Errorf("Residuals[%d] = %v; want 0 in seed window", i, r[i])
		}
	}
}

// TestHoltWinters_NextForecastInRange asserts that the one-step-ahead
// forecast on a clean seasonal series falls inside the value range
// of the last cycle plus a small margin.
func TestHoltWinters_NextForecastInRange(t *testing.T) {
	period := 24
	ys := pureSeasonalNoTrend(period*3, period, 1.0, 0.1)
	fit, err := HoltWinters(ys, period, 0.3, 0.1, 0.1)
	if err != nil {
		t.Fatalf("HoltWinters: %v", err)
	}
	// Range of the last cycle.
	min, max := math.Inf(1), math.Inf(-1)
	for _, v := range ys[len(ys)-period:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	// Allow a 25% margin: HW's NextForecast extrapolates using the
	// trend term so it can sit a hair outside the historic range.
	span := max - min
	lo, hi := min-0.25*span, max+0.25*span
	if fit.NextForecast < lo || fit.NextForecast > hi {
		t.Errorf("NextForecast = %.4f; outside [%.4f, %.4f]", fit.NextForecast, lo, hi)
	}
}

// TestInitialAdditiveSeasonals_SumsToZero asserts the additive
// invariant: the seed seasonals sum (approximately) to zero.
func TestInitialAdditiveSeasonals_SumsToZero(t *testing.T) {
	period := 24
	ys := pureSeasonalNoTrend(period*2, period, 1.0, 0.1)
	seas := initialAdditiveSeasonals(ys, period)
	if len(seas) != period {
		t.Fatalf("len(seas) = %d; want %d", len(seas), period)
	}
	var sum float64
	for _, s := range seas {
		sum += s
	}
	if !floatNear(sum, 0, 1e-9) {
		t.Errorf("sum(seasonals) = %.6e; want ~0", sum)
	}
}

// TestInitialTrend_FlatSeriesIsZero asserts the trend seed is zero
// when the input has no drift.
func TestInitialTrend_FlatSeriesIsZero(t *testing.T) {
	period := 12
	ys := make([]float64, period*2)
	for i := range ys {
		ys[i] = 0.95 // flat
	}
	if got := initialTrend(ys, period); !floatNear(got, 0, 1e-12) {
		t.Errorf("initialTrend = %v; want 0 on flat series", got)
	}
}

// TestInitialTrend_PositiveSlope asserts the trend seed is positive
// when y monotonically rises.
func TestInitialTrend_PositiveSlope(t *testing.T) {
	period := 4
	// y_i = 0.1 * i; slope between cycles is +period * 0.1 / period = 0.1
	ys := []float64{
		0.0, 0.1, 0.2, 0.3, // cycle 0
		0.4, 0.5, 0.6, 0.7, // cycle 1
	}
	got := initialTrend(ys, period)
	// Each pairwise slope (y[period+i]-y[i])/period == 0.1, averaged
	// over 4 phases stays 0.1.
	if !floatNear(got, 0.1, 1e-12) {
		t.Errorf("initialTrend = %v; want 0.1", got)
	}
}

// TestMean_EmptyReturnsZero asserts the empty-input guard.
func TestMean_EmptyReturnsZero(t *testing.T) {
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %v; want 0", got)
	}
	if got := mean([]float64{}); got != 0 {
		t.Errorf("mean([]) = %v; want 0", got)
	}
}

// TestMean_KnownInputs walks small known cases.
func TestMean_KnownInputs(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{1}, 1},
		{[]float64{1, 2, 3}, 2},
		{[]float64{-1, 1}, 0},
		{[]float64{2.5, 3.5}, 3.0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			if got := mean(tc.in); !floatNear(got, tc.want, 1e-12) {
				t.Errorf("mean(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
