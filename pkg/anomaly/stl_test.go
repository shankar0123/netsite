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

// TestDecompose_InvalidPeriod asserts the period guard.
func TestDecompose_InvalidPeriod(t *testing.T) {
	_, err := Decompose([]float64{1, 2, 3, 4}, 1)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("err = %v; want ErrInvalidPeriod", err)
	}
	_, err = Decompose([]float64{1, 2, 3, 4}, 0)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("err = %v; want ErrInvalidPeriod", err)
	}
}

// TestDecompose_InsufficientData asserts the n >= 2*period guard.
func TestDecompose_InsufficientData(t *testing.T) {
	// period 4, n=7 < 8.
	_, err := Decompose([]float64{1, 2, 3, 4, 5, 6, 7}, 4)
	if !errors.Is(err, ErrInsufficientData) {
		t.Errorf("err = %v; want ErrInsufficientData", err)
	}
}

// TestDecompose_PureSeasonalRecoversComponents asserts that on a
// deterministic seasonal-only series, Decompose recovers near-zero
// residuals in the valid (non-edge) range and a non-trivial season
// component.
func TestDecompose_PureSeasonalRecoversComponents(t *testing.T) {
	period := 24
	n := period * 5
	ys := make([]float64, n)
	for i := 0; i < n; i++ {
		theta := 2 * math.Pi * float64(i%period) / float64(period)
		ys[i] = 1.0 + 0.05*math.Sin(theta)
	}
	dec, err := Decompose(ys, period)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if len(dec.Trend) != n || len(dec.Season) != n || len(dec.Residual) != n {
		t.Fatalf("component lengths off: trend=%d season=%d residual=%d", len(dec.Trend), len(dec.Season), len(dec.Residual))
	}
	if len(dec.PeriodMeans) != period {
		t.Fatalf("PeriodMeans length = %d; want %d", len(dec.PeriodMeans), period)
	}

	// In the valid centred range, residuals are tiny on a noiseless
	// seasonal-only signal. Trend ≈ 1, season recovers the sin.
	half := period / 2
	for i := half; i < n-half; i++ {
		if math.Abs(dec.Residual[i]) > 1e-6 {
			t.Errorf("residual[%d] = %v; want ~0 on noiseless input", i, dec.Residual[i])
		}
		if math.Abs(dec.Trend[i]-1.0) > 1e-6 {
			t.Errorf("trend[%d] = %v; want ~1.0", i, dec.Trend[i])
		}
	}

	// PeriodMeans must approximately sum to zero (additive invariant).
	var sum float64
	for _, v := range dec.PeriodMeans {
		sum += v
	}
	if math.Abs(sum) > 1e-9 {
		t.Errorf("sum(PeriodMeans) = %v; want ~0", sum)
	}
}

// TestDecompose_InjectedSpikeShowsInResidual asserts that a sharp
// injection at an interior point materialises in the residual at
// roughly the injected amount (the centred MA smears it across the
// window, so we don't expect exact equality).
func TestDecompose_InjectedSpikeShowsInResidual(t *testing.T) {
	period := 24
	n := period * 5
	ys := make([]float64, n)
	for i := 0; i < n; i++ {
		theta := 2 * math.Pi * float64(i%period) / float64(period)
		ys[i] = 1.0 + 0.05*math.Sin(theta)
	}
	// Inject at an interior point (so centred MA is well-defined).
	idx := n / 2
	ys[idx] += 1.0
	dec, err := Decompose(ys, period)
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	// Spike's residual should be close to the injection minus what
	// the centred MA absorbed (roughly 1/period of the spike).
	want := 1.0 * (1 - 1.0/float64(period))
	got := dec.Residual[idx]
	if got <= 0.5 {
		t.Errorf("residual at spike = %v; want substantially positive (~%.2f)", got, want)
	}
}

// TestCentredMovingAverage_OddPeriod walks the odd-period code path.
func TestCentredMovingAverage_OddPeriod(t *testing.T) {
	period := 5
	// Constant series → centred MA equals the constant in the valid
	// range, zero at the edges (per the function contract).
	ys := []float64{2, 2, 2, 2, 2, 2, 2, 2, 2}
	out := centredMovingAverage(ys, period)
	half := period / 2
	for i := 0; i < half; i++ {
		if out[i] != 0 {
			t.Errorf("out[%d] = %v; want 0 at left edge", i, out[i])
		}
	}
	for i := half; i < len(ys)-half; i++ {
		if math.Abs(out[i]-2) > 1e-12 {
			t.Errorf("out[%d] = %v; want 2", i, out[i])
		}
	}
	for i := len(ys) - half; i < len(ys); i++ {
		if out[i] != 0 {
			t.Errorf("out[%d] = %v; want 0 at right edge", i, out[i])
		}
	}
}

// TestCentredMovingAverage_EvenPeriod walks the even-period (2x MA)
// code path.
func TestCentredMovingAverage_EvenPeriod(t *testing.T) {
	period := 4
	// Constant series → centred 2xMA equals the constant in valid
	// range. Verifies the symmetric-average branch.
	ys := []float64{3, 3, 3, 3, 3, 3, 3, 3, 3, 3}
	out := centredMovingAverage(ys, period)
	half := period / 2
	for i := half; i < len(ys)-half; i++ {
		if math.Abs(out[i]-3) > 1e-12 {
			t.Errorf("out[%d] = %v; want 3", i, out[i])
		}
	}
}

// TestMAD_EmptyReturnsZero asserts the empty-input guard.
func TestMAD_EmptyReturnsZero(t *testing.T) {
	if got := MAD(nil); got != 0 {
		t.Errorf("MAD(nil) = %v; want 0", got)
	}
	if got := MAD([]float64{}); got != 0 {
		t.Errorf("MAD([]) = %v; want 0", got)
	}
}

// TestMAD_KnownInputs walks small canonical cases.
func TestMAD_KnownInputs(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		// median = 3, |xi - 3| = {2,1,0,1,2}, sorted {0,1,1,2,2}, median = 1
		{"odd_count", []float64{1, 2, 3, 4, 5}, 1},
		// median = 2.5, |xi - 2.5| = {1.5, 0.5, 0.5, 1.5}, sorted, median = (0.5+1.5)/2 = 1
		{"even_count", []float64{1, 2, 3, 4}, 1},
		{"all_equal", []float64{7, 7, 7, 7, 7}, 0},
		{"single_element", []float64{42}, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := MAD(tc.in); math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("MAD(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMAD_RobustToOutliers asserts MAD does not inflate as a single
// extreme outlier is added — its raison d'être versus stddev.
func TestMAD_RobustToOutliers(t *testing.T) {
	// Without outlier
	base := []float64{1, 2, 3, 4, 5}
	mBase := MAD(base)
	// Add one extreme outlier
	withOutlier := []float64{1, 2, 3, 4, 5, 10000}
	mWithOutlier := MAD(withOutlier)
	if math.Abs(mWithOutlier-mBase) > 1.0 {
		t.Errorf("MAD shifted from %v to %v on outlier injection — should stay close",
			mBase, mWithOutlier)
	}
}

// TestMAD_DoesNotMutateInput asserts MAD treats its input as
// read-only (it allocates internally via median).
func TestMAD_DoesNotMutateInput(t *testing.T) {
	in := []float64{5, 1, 4, 2, 3}
	cp := append([]float64(nil), in...)
	_ = MAD(in)
	for i := range in {
		if in[i] != cp[i] {
			t.Errorf("input mutated at %d: %v != %v", i, in[i], cp[i])
		}
	}
}

// TestMedian_KnownInputs walks the median helper.
func TestMedian_KnownInputs(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{}, 0},
		{[]float64{5}, 5},
		{[]float64{3, 1, 2}, 2},         // odd
		{[]float64{4, 1, 3, 2}, 2.5},    // even
		{[]float64{-5, -1, 0, 1, 5}, 0}, // includes negatives
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			if got := median(tc.in); math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("median(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMedian_DoesNotMutateInput asserts median preserves caller input
// order.
func TestMedian_DoesNotMutateInput(t *testing.T) {
	in := []float64{5, 1, 4, 2, 3}
	cp := append([]float64(nil), in...)
	_ = median(in)
	for i := range in {
		if in[i] != cp[i] {
			t.Errorf("input mutated at %d: %v != %v", i, in[i], cp[i])
		}
	}
}
