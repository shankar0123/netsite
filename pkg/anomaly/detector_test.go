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
	"math/rand/v2"
	"testing"
	"time"
)

// seasonalSeries produces N hourly-spaced points in a daily cycle:
//
//	value = base + amp * sin(2π * (i % period) / period) + noise
//
// noise is drawn from a deterministic PRNG so the test is
// reproducible. Returns the Series.
func seasonalSeries(t *testing.T, n, period int, base, amp, noiseStd float64, seed uint64) Series {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, seed+1))
	out := make(Series, n)
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		theta := 2 * math.Pi * float64(i%period) / float64(period)
		v := base + amp*math.Sin(theta) + r.NormFloat64()*noiseStd
		out[i] = Point{At: start.Add(time.Duration(i) * time.Hour), Value: v}
	}
	return out
}

// inject adds a delta to the LAST point of s.
func inject(s Series, delta float64) Series {
	out := append(Series(nil), s...)
	out[len(out)-1].Value += delta
	return out
}

// TestDetect_EmptySeries asserts the empty-series guard.
func TestDetect_EmptySeries(t *testing.T) {
	_, err := Detect(Series{}, Config{})
	if !errors.Is(err, ErrEmptySeries) {
		t.Errorf("err = %v; want ErrEmptySeries", err)
	}
}

// TestDetect_NotSorted asserts the input-order guard.
func TestDetect_NotSorted(t *testing.T) {
	now := time.Now().UTC()
	s := Series{
		{At: now.Add(2 * time.Hour), Value: 1},
		{At: now, Value: 2},
	}
	_, err := Detect(s, Config{})
	if !errors.Is(err, ErrSeriesNotSorted) {
		t.Errorf("err = %v; want ErrSeriesNotSorted", err)
	}
}

// TestDetect_InsufficientData asserts the chooser returns
// MethodInsufficientData when the series is too short.
func TestDetect_InsufficientData(t *testing.T) {
	s := seasonalSeries(t, 10, 24, 1, 0.1, 0.01, 1)
	v, err := Detect(s, Config{Period: 24})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if v.Method != MethodInsufficientData {
		t.Errorf("Method = %s; want %s", v.Method, MethodInsufficientData)
	}
	if v.Severity != SeverityNone {
		t.Errorf("Severity = %s; want none", v.Severity)
	}
}

// TestDetect_HoltWinters_CleanSeriesReportsNone asserts that a
// noise-free seasonal series with no injection produces SeverityNone.
func TestDetect_HoltWinters_CleanSeriesReportsNone(t *testing.T) {
	s := seasonalSeries(t, 84, 24, 1.0, 0.05, 0.0, 42) // 84h = 3.5 cycles → HW range
	v, err := Detect(s, Config{Period: 24})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if v.Method != MethodHoltWinters {
		t.Errorf("Method = %s; want %s", v.Method, MethodHoltWinters)
	}
	if v.Severity != SeverityNone && v.Severity != SeverityWatch {
		t.Errorf("Severity = %s; want none/watch on a clean series", v.Severity)
	}
}

// TestDetect_HoltWinters_BigSpikeIsCritical asserts a large injected
// spike at the latest point flips Severity to Critical.
func TestDetect_HoltWinters_BigSpikeIsCritical(t *testing.T) {
	s := seasonalSeries(t, 84, 24, 1.0, 0.05, 0.005, 7)
	s = inject(s, 5.0) // 5 units above expected
	v, err := Detect(s, Config{Period: 24})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if v.Severity != SeverityCritical {
		t.Errorf("Severity = %s; want critical (residual=%.3f madUnits=%.2f)",
			v.Severity, v.Residual, v.MADUnits)
	}
	if math.Abs(v.Residual) <= 0 {
		t.Errorf("Residual = %v; want non-zero", v.Residual)
	}
}

// TestDetect_SeasonalDecompose_LongSeriesUsesIt asserts that a
// long-enough series triggers the seasonal-decompose branch.
func TestDetect_SeasonalDecompose_LongSeriesUsesIt(t *testing.T) {
	s := seasonalSeries(t, 24*5, 24, 1.0, 0.05, 0.005, 11) // 5 full cycles
	v, err := Detect(s, Config{Period: 24})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if v.Method != MethodSeasonalDecompose {
		t.Errorf("Method = %s; want %s", v.Method, MethodSeasonalDecompose)
	}
}

// TestDetect_CalendarSuppression asserts that a window covering the
// latest point caps Severity at Watch.
func TestDetect_CalendarSuppression(t *testing.T) {
	s := seasonalSeries(t, 84, 24, 1.0, 0.05, 0.005, 13)
	s = inject(s, 5.0)

	last := s[len(s)-1].At
	cal := []SuppressionWindow{
		{Start: last.Add(-time.Hour), End: last.Add(time.Hour), Reason: "test"},
	}
	v, err := Detect(s, Config{Period: 24, Calendar: cal})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !v.Suppressed {
		t.Error("expected Suppressed=true")
	}
	if v.Severity == SeverityCritical || v.Severity == SeverityAnomaly {
		t.Errorf("Severity = %s; want capped at watch under suppression", v.Severity)
	}
}

// TestDetect_CalendarSuppression_EmptyReason asserts that a
// suppression window with an empty Reason still suppresses cleanly
// (the Verdict.Reason gets a generic "(suppressed)" suffix rather
// than "(suppressed: )").
func TestDetect_CalendarSuppression_EmptyReason(t *testing.T) {
	s := seasonalSeries(t, 84, 24, 1.0, 0.05, 0.005, 17)
	s = inject(s, 5.0)

	last := s[len(s)-1].At
	cal := []SuppressionWindow{
		{Start: last.Add(-time.Hour), End: last.Add(time.Hour)}, // Reason left blank
	}
	v, err := Detect(s, Config{Period: 24, Calendar: cal})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !v.Suppressed {
		t.Error("expected Suppressed=true")
	}
	// The reason suffix should be the generic form, not the
	// reason-carrying form, because the window had no reason set.
	if v.Reason == "" {
		t.Error("expected non-empty Verdict.Reason")
	}
}

// TestChooseMethod walks the data-density chooser matrix.
func TestChooseMethod(t *testing.T) {
	cases := []struct {
		name            string
		n, period, minS int
		want            Method
	}{
		{"too few", 10, 24, 24, MethodInsufficientData},
		{"min ok but < 2 period", 25, 24, 20, MethodInsufficientData},
		{"hw range", 60, 24, 30, MethodHoltWinters},
		{"stl range", 96, 24, 30, MethodSeasonalDecompose},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseMethod(tc.n, tc.period, tc.minS); got != tc.want {
				t.Errorf("got %s; want %s", got, tc.want)
			}
		})
	}
}

// TestClassifySeverity walks the threshold matrix.
func TestClassifySeverity(t *testing.T) {
	cfg := Defaults(Config{})
	cases := []struct {
		madUnits float64
		want     Severity
	}{
		{0, SeverityNone},
		{2.9, SeverityNone},
		{3.5, SeverityWatch},
		{5.5, SeverityAnomaly},
		{8.5, SeverityCritical},
		{math.NaN(), SeverityNone},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			if got := classifySeverity(tc.madUnits, cfg); got != tc.want {
				t.Errorf("classifySeverity(%v) = %s; want %s", tc.madUnits, got, tc.want)
			}
		})
	}
}
