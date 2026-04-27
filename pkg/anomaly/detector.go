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
	"time"
)

// What: the public entry point. Takes a Series and a Config,
// chooses a method (Holt-Winters when the series has at least
// 2*period samples and reasonable density; classical seasonal
// decomposition when more than 3*period; insufficient_data
// otherwise), runs it, classifies the latest point's residual
// against MAD thresholds, applies calendar suppression, and returns
// a Verdict.
//
// How: the chooser logic favors Holt-Winters at the lower data-
// density end because its one-step forecast is what an alert engine
// actually wants. STL-style decomposition kicks in when there's
// enough data that a stable seasonal pattern is reliable; its
// residual series is more comparable across time than HW's because
// HW's level/trend keep adapting.
//
// Why two methods rather than one: SLI series differ. Some are
// dominated by drift (level changes); HW handles those via its
// trend term. Others are dominated by stable seasonality with
// little drift; STL's per-phase mean is more interpretable. The
// chooser picks one per call so the Verdict carries a single
// explainable answer.
//
// Why we don't run both and ensemble: ensembling adds a free
// parameter (how to combine) that we'd need calibration data to
// tune. Phase 1 will revisit.

// Detect runs the full pipeline against a Series. Returns
// ErrEmptySeries / ErrSeriesNotSorted on bad input.
func Detect(series Series, cfg Config) (Verdict, error) {
	if len(series) == 0 {
		return Verdict{}, ErrEmptySeries
	}
	if !isSortedAsc(series) {
		return Verdict{}, ErrSeriesNotSorted
	}
	cfg = Defaults(cfg)
	now := time.Now().UTC()

	values := make([]float64, len(series))
	for i, p := range series {
		values[i] = p.Value
	}
	latest := series[len(series)-1]

	method := chooseMethod(len(values), cfg.Period, cfg.MinSamples)
	if method == MethodInsufficientData {
		return Verdict{
			Method:      MethodInsufficientData,
			Severity:    SeverityNone,
			LatestPoint: latest,
			Forecast:    latest.Value,
			Reason:      fmt.Sprintf("series length %d below MinSamples %d", len(values), cfg.MinSamples),
			EvaluatedAt: now,
		}, nil
	}

	v := Verdict{
		Method:      method,
		LatestPoint: latest,
		EvaluatedAt: now,
	}

	switch method {
	case MethodHoltWinters:
		// chooseMethod guarantees n >= 2*period and period > 1, so
		// HoltWinters cannot return ErrInsufficientData or
		// ErrInvalidPeriod from here. Any error is a programmer bug
		// and propagates to the caller.
		fit, err := HoltWinters(values, cfg.Period, cfg.Alpha, cfg.Beta, cfg.Gamma)
		if err != nil {
			return Verdict{}, fmt.Errorf("anomaly: holt-winters: %w", err)
		}
		// In-sample residuals from after the seed window.
		resid := make([]float64, 0, len(values)-cfg.Period)
		for i := cfg.Period; i < len(values); i++ {
			resid = append(resid, values[i]-fit.InSample[i])
		}
		v.MAD = MAD(resid)
		// The "latest residual" is the one for the latest input point —
		// use the just-computed in-sample fit at the last index.
		v.Forecast = fit.InSample[len(values)-1]
		v.Residual = latest.Value - v.Forecast
		v.Reason = fmt.Sprintf("Holt-Winters: level=%.4f trend=%.4f season[%d]=%.4f",
			fit.Level, fit.Trend, (len(values)-1)%cfg.Period,
			fit.Seasonals[(len(values)-1)%cfg.Period])

	case MethodSeasonalDecompose:
		// Same chooseMethod invariants apply — n >= 4*period and
		// period > 1 — so Decompose cannot legitimately error here.
		dec, err := Decompose(values, cfg.Period)
		if err != nil {
			return Verdict{}, fmt.Errorf("anomaly: decompose: %w", err)
		}
		// Residuals only valid in the centred-MA range; drop edges.
		half := cfg.Period / 2
		validResid := make([]float64, 0, len(values)-2*half)
		for i := half; i < len(values)-half; i++ {
			validResid = append(validResid, dec.Residual[i])
		}
		v.MAD = MAD(validResid)
		// For the latest point at the right edge, trend is undefined.
		// Substitute the last well-defined trend value as the
		// forecast plus the latest season component. lastValidIdx is
		// guaranteed >= 0 because chooseMethod ensures n >= 4*period
		// and half = period/2, so n - half - 1 >= 4*period - period/2 - 1 > 0.
		lastValidIdx := len(values) - half - 1
		trendAt := dec.Trend[lastValidIdx]
		seasonAt := dec.Season[len(values)-1]
		v.Forecast = trendAt + seasonAt
		v.Residual = latest.Value - v.Forecast
		v.Reason = fmt.Sprintf("seasonal-decompose (period=%d, MAD=%.4f)", cfg.Period, v.MAD)
	}

	if v.MAD > 0 {
		v.MADUnits = math.Abs(v.Residual) / v.MAD
	}
	v.Severity = classifySeverity(v.MADUnits, cfg)

	// Calendar suppression. Compute, then optionally cap severity.
	if len(cfg.Calendar) > 0 {
		cal := NewCalendar(cfg.Calendar)
		if suppressed, reason := cal.Suppresses(latest.At); suppressed {
			v.Suppressed = true
			if v.Severity == SeverityAnomaly || v.Severity == SeverityCritical {
				v.Severity = SeverityWatch
			}
			if reason != "" {
				v.Reason = v.Reason + " (suppressed: " + reason + ")"
			} else {
				v.Reason = v.Reason + " (suppressed)"
			}
		}
	}

	return v, nil
}

// chooseMethod is the data-density chooser.
//
//	n < MinSamples            → InsufficientData
//	2*period <= n < 4*period  → HoltWinters
//	n >= 4*period             → SeasonalDecompose
//
// The threshold of 4 cycles for STL is a tuning choice: with three
// or fewer cycles, the per-phase mean is too noisy; with four or
// more it's stable enough to surface real residuals.
func chooseMethod(n, period, minSamples int) Method {
	if n < minSamples {
		return MethodInsufficientData
	}
	if n < 2*period {
		return MethodInsufficientData
	}
	if n < 4*period {
		return MethodHoltWinters
	}
	return MethodSeasonalDecompose
}

// classifySeverity maps MADUnits to a Severity label using the
// configured thresholds. NaN MAD (e.g. constant series) → None.
func classifySeverity(madUnits float64, cfg Config) Severity {
	if math.IsNaN(madUnits) || madUnits == 0 {
		return SeverityNone
	}
	switch {
	case madUnits >= cfg.MADThresholdCritical:
		return SeverityCritical
	case madUnits >= cfg.MADThresholdAnomaly:
		return SeverityAnomaly
	case madUnits >= cfg.MADThresholdWatch:
		return SeverityWatch
	default:
		return SeverityNone
	}
}

// isSortedAsc reports whether series is sorted ascending by At.
// Equality is allowed (two points at the same instant); strict
// ordering is not required because canary results from multiple
// POPs can legitimately share a timestamp.
func isSortedAsc(s Series) bool {
	for i := 1; i < len(s); i++ {
		if s[i].At.Before(s[i-1].At) {
			return false
		}
	}
	return true
}
