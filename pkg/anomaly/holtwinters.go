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
	"fmt"
	"math"
)

// What: additive triple-exponential smoothing — the Holt-Winters
// algorithm. It maintains three exponentially-weighted state
// components (level, trend, season) and produces a one-step forecast
// that respects the seasonal pattern.
//
// How: initialise level / trend / seasonals from the first full
// period of data, then iterate the standard recursive equations:
//
//	level_t  = alpha * (y_t - season_{t-period}) + (1 - alpha) * (level_{t-1} + trend_{t-1})
//	trend_t  = beta  * (level_t - level_{t-1})    + (1 - beta)  * trend_{t-1}
//	season_t = gamma * (y_t - level_t)            + (1 - gamma) * season_{t-period}
//
//	forecast_{t+h} = level_t + h * trend_t + season_{t+h-period}
//
// Returns the in-sample fit (the forecast for each y_t given the
// model fitted up to t-1) and the one-step-ahead forecast for the
// time point right after the input.
//
// Why additive rather than multiplicative: success-rate SLIs sit
// between 0 and 1; multiplicative seasonality misbehaves near zero
// (a near-zero season component multiplied by a small level
// produces meaningless forecasts). Additive is well-defined across
// the whole [0,1] range and slightly easier to debug. Tradeoff: if
// seasonality scales with level (e.g., flow rate over weekly cycles
// where weekday is 10x weekend), multiplicative wins. We add the
// multiplicative variant when a real series demands it.

// HoltWintersFit is the output of HoltWinters: per-input-point
// forecasts plus the next-step forecast and the latest model state.
type HoltWintersFit struct {
	// InSample[i] is the forecast for y[i] given the model fitted
	// through y[i-1]. Length equals len(input). The first `period`
	// entries equal the input (zero-residual init).
	InSample []float64

	// NextForecast is the prediction for the time point one step
	// after the last input.
	NextForecast float64

	// Level / Trend at the last step. Operators eyeballing the fit
	// often want these.
	Level float64
	Trend float64

	// Seasonals is the final estimate of the seasonal component,
	// length == period.
	Seasonals []float64
}

// HoltWinters fits the additive triple-exponential smoothing model
// to ys with the given period. Returns ErrInsufficientData if
// len(ys) < 2 * period (the algorithm needs at least two full
// cycles to initialise reasonable seasonal estimates).
//
// alpha, beta, gamma must each be in (0, 1). Validation is the
// caller's responsibility; pkg/anomaly.Defaults ensures sensible
// values when the user passes zero.
func HoltWinters(ys []float64, period int, alpha, beta, gamma float64) (HoltWintersFit, error) {
	n := len(ys)
	if period <= 1 {
		return HoltWintersFit{}, ErrInvalidPeriod
	}
	if n < 2*period {
		return HoltWintersFit{}, fmt.Errorf("%w: holt-winters needs >= 2*period (%d) samples; got %d",
			ErrInsufficientData, 2*period, n)
	}

	// Initial seasonals: average each phase across the first two
	// cycles, subtracted from the cycle's mean. This yields
	// zero-mean season components per cycle, which is the additive
	// invariant the recursion expects.
	seasonals := initialAdditiveSeasonals(ys, period)

	// Initial level: mean of the first period.
	level := mean(ys[:period])
	// Initial trend: average pairwise slope across the first two
	// periods.
	trend := initialTrend(ys, period)

	inSample := make([]float64, n)
	// First period: no model yet; treat as fit-equals-observation.
	copy(inSample[:period], ys[:period])

	for t := period; t < n; t++ {
		// Forecast for y[t] given state at t-1.
		inSample[t] = level + trend + seasonals[t%period]

		yt := ys[t]
		prevLevel := level
		level = alpha*(yt-seasonals[t%period]) + (1-alpha)*(level+trend)
		trend = beta*(level-prevLevel) + (1-beta)*trend
		seasonals[t%period] = gamma*(yt-level) + (1-gamma)*seasonals[t%period]
	}

	// One-step-ahead forecast: level + trend + season for the next
	// phase index.
	next := level + trend + seasonals[n%period]

	return HoltWintersFit{
		InSample:     inSample,
		NextForecast: next,
		Level:        level,
		Trend:        trend,
		Seasonals:    seasonals,
	}, nil
}

// initialAdditiveSeasonals computes per-phase season components by
// averaging the phase across the first two complete cycles, then
// subtracting the cycle mean so each cycle's season components sum
// to zero (the additive invariant).
func initialAdditiveSeasonals(ys []float64, period int) []float64 {
	out := make([]float64, period)
	cycles := 2
	for i := 0; i < period; i++ {
		var sum float64
		for c := 0; c < cycles; c++ {
			sum += ys[c*period+i]
		}
		out[i] = sum / float64(cycles)
	}
	// Subtract the mean of out so the seasonals sum to zero.
	m := mean(out)
	for i := range out {
		out[i] -= m
	}
	return out
}

// initialTrend averages slopes across the first two periods.
func initialTrend(ys []float64, period int) float64 {
	var sum float64
	for i := 0; i < period; i++ {
		sum += (ys[period+i] - ys[i]) / float64(period)
	}
	return sum / float64(period)
}

// mean is arithmetic mean. Returns 0 for an empty slice; callers
// guard against that upstream.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// Residuals returns y[i] - fit.InSample[i] for the in-sample range.
// The first `period` entries of fit.InSample mirror the input, so
// their residuals are zero by construction; we drop those when
// computing MAD downstream.
func (f HoltWintersFit) Residuals(ys []float64) []float64 {
	out := make([]float64, len(ys))
	for i := range ys {
		out[i] = ys[i] - f.InSample[i]
	}
	return out
}

// _ = math.NaN ensures the math import is referenced even if a
// future refactor drops the only use site. Cheaper than juggling
// imports across files.
var _ = math.NaN
