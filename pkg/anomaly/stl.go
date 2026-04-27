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
	"sort"
)

// What: classical seasonal decomposition — a time series y_t is
// modeled as
//
//	y_t = trend_t + season_t + residual_t
//
// where trend is a centred moving average of length `period`,
// season is the per-phase mean of (y - trend), and residual is
// what's left.
//
// How: implemented in three passes. (1) centred moving average of
// span `period` produces the trend (NaN-equivalent at the edges
// where the average has insufficient data; we leave those as zero
// in trend and skip them downstream). (2) detrend by subtracting
// trend and average per-phase across the series to get one season
// value per phase. (3) residual = y - trend - season. The residuals
// are what the detector compares against MAD thresholds.
//
// Why a simplified STL (not full STL-LOESS): the canonical STL
// algorithm (Cleveland et al. 1990) replaces the moving average and
// the per-phase average with two iterated LOESS smoothers. LOESS is
// a locally-weighted regression with bandwidth and degree
// parameters; correctly implementing it is ~500 lines of careful
// numerics against published reference data. This simplification
// sacrifices the LOESS edge-handling and adaptiveness for ~40 lines
// of code that handles the operationally-common case (canary
// series with an obvious daily/weekly cycle) just fine. Full STL
// arrives in Phase 1 once we have enough real-world series to
// calibrate the LOESS bandwidth choice and verify against
// independent reference implementations.
//
// What works well with this simplification:
//   - Series with a single, stable seasonal period (daily for
//     hourly data, weekly for daily data).
//   - Series where edge effects are tolerable — we trim the first
//     and last `period/2` points from MAD computation.
//
// What this simplification does not handle as cleanly as full STL:
//   - Series where the seasonal *shape* drifts over time. Full STL
//     re-estimates the season at each iteration; we estimate it
//     once.
//   - Sub-daily series with multiple overlapping periods (e.g.,
//     daily + weekly). For now we recommend running the detector
//     against one period at a time.

// Decomposition holds the three components plus the detrended /
// deseasonalised residual series. Lengths are all len(input).
type Decomposition struct {
	Trend    []float64
	Season   []float64
	Residual []float64
	// PeriodMeans is the per-phase season vector (length == period).
	// Operators inspecting the season component find this useful.
	PeriodMeans []float64
}

// Decompose returns the STL-style additive decomposition of ys with
// the given seasonality period. Returns ErrInsufficientData when
// len(ys) < 2 * period.
func Decompose(ys []float64, period int) (Decomposition, error) {
	if period <= 1 {
		return Decomposition{}, ErrInvalidPeriod
	}
	n := len(ys)
	if n < 2*period {
		return Decomposition{}, ErrInsufficientData
	}

	trend := centredMovingAverage(ys, period)
	// Detrended series. Edges where trend is undefined hold zero
	// detrend, which biases the per-phase mean toward 0; we
	// compensate by counting only finite contributions per phase.
	detrended := make([]float64, n)
	count := make([]int, period)
	sumPerPhase := make([]float64, period)

	half := period / 2
	for i := half; i < n-half; i++ {
		detrended[i] = ys[i] - trend[i]
		ph := i % period
		sumPerPhase[ph] += detrended[i]
		count[ph]++
	}

	// Per-phase mean (raw). We then re-centre so the season
	// components sum to zero — the additive invariant.
	periodMeans := make([]float64, period)
	for ph := 0; ph < period; ph++ {
		if count[ph] > 0 {
			periodMeans[ph] = sumPerPhase[ph] / float64(count[ph])
		}
	}
	mean := 0.0
	for _, v := range periodMeans {
		mean += v
	}
	mean /= float64(period)
	for ph := range periodMeans {
		periodMeans[ph] -= mean
	}

	// Project the season back over the full series.
	season := make([]float64, n)
	for i := range ys {
		season[i] = periodMeans[i%period]
	}

	// Residual = y - trend - season; zero at the edges where trend
	// is undefined.
	residual := make([]float64, n)
	for i := half; i < n-half; i++ {
		residual[i] = ys[i] - trend[i] - season[i]
	}

	return Decomposition{
		Trend:       trend,
		Season:      season,
		Residual:    residual,
		PeriodMeans: periodMeans,
	}, nil
}

// centredMovingAverage returns a centred moving average of span
// `period` over ys. Edges where the window is incomplete hold zero;
// callers must skip them (the indices half ≤ i < n-half are valid).
//
// For even periods we apply the standard "2x period MA" — average
// the moving averages of length period at offsets 0 and 1 — so the
// result is centred between observations rather than to one side.
func centredMovingAverage(ys []float64, period int) []float64 {
	n := len(ys)
	out := make([]float64, n)
	half := period / 2

	if period%2 == 1 {
		// Odd period: simple symmetric MA.
		for i := half; i < n-half; i++ {
			var s float64
			for j := -half; j <= half; j++ {
				s += ys[i+j]
			}
			out[i] = s / float64(period)
		}
		return out
	}

	// Even period: 2x period MA — average two adjacent length-period
	// MAs to centre between observations.
	periodMA := make([]float64, n)
	for i := half - 1; i < n-half; i++ {
		var s float64
		for j := -half + 1; j <= half; j++ {
			s += ys[i+j]
		}
		periodMA[i] = s / float64(period)
	}
	for i := half; i < n-half; i++ {
		out[i] = (periodMA[i-1] + periodMA[i]) / 2
	}
	return out
}

// MAD returns the median absolute deviation of xs about the median.
// MAD is the standard robust scale estimator: 1.4826 * MAD ≈ sigma
// for normal data, and unlike sigma it tolerates a small fraction of
// outliers without inflating. Returns 0 for an empty input.
func MAD(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	med := median(xs)
	abs := make([]float64, len(xs))
	for i, x := range xs {
		d := x - med
		if d < 0 {
			d = -d
		}
		abs[i] = d
	}
	return median(abs)
}

// median copies and sorts xs to find the median. Allocates because
// callers expect xs unchanged. For Phase 0 series of < 1k points,
// the alloc is in the noise.
func median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
