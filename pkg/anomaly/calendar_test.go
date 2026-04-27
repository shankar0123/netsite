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
	"testing"
	"time"
)

// utc returns a deterministic UTC time at the given hour for use in
// table-driven tests. Anchoring on a fixed date keeps cases readable
// and avoids any wall-clock interaction.
func utc(hour int) time.Time {
	return time.Date(2026, 4, 27, hour, 0, 0, 0, time.UTC)
}

// TestCalendar_EmptyNeverSuppresses asserts the empty-Calendar
// short-circuit.
func TestCalendar_EmptyNeverSuppresses(t *testing.T) {
	c := NewCalendar(nil)
	if c.Len() != 0 {
		t.Errorf("Len() = %d; want 0", c.Len())
	}
	if got, _ := c.Suppresses(time.Now()); got {
		t.Error("empty Calendar should never suppress")
	}
}

// TestCalendar_SortedAtConstruction asserts that out-of-order input
// is sorted internally so binary search is correct.
func TestCalendar_SortedAtConstruction(t *testing.T) {
	w := []SuppressionWindow{
		{Start: utc(10), End: utc(11), Reason: "B"},
		{Start: utc(2), End: utc(3), Reason: "A"},
		{Start: utc(20), End: utc(21), Reason: "C"},
	}
	c := NewCalendar(w)
	if c.Len() != 3 {
		t.Fatalf("Len() = %d; want 3", c.Len())
	}
	// Spot check: t=02:30 falls in window A regardless of input
	// order; binary search must locate it.
	got, reason := c.Suppresses(utc(2).Add(30 * time.Minute))
	if !got || reason != "A" {
		t.Errorf("Suppresses at 02:30 = (%v, %q); want (true, %q)", got, reason, "A")
	}
}

// TestCalendar_HalfOpenSemantics asserts [Start, End): Start
// suppressed, End not suppressed.
func TestCalendar_HalfOpenSemantics(t *testing.T) {
	w := []SuppressionWindow{
		{Start: utc(10), End: utc(12), Reason: "lunch"},
	}
	c := NewCalendar(w)

	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before_start", utc(9), false},
		{"exactly_start", utc(10), true},
		{"interior", utc(11), true},
		{"just_before_end", utc(12).Add(-time.Nanosecond), true},
		{"exactly_end", utc(12), false},
		{"after_end", utc(13), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := c.Suppresses(tc.t); got != tc.want {
				t.Errorf("Suppresses(%v) = %v; want %v", tc.t, got, tc.want)
			}
		})
	}
}

// TestCalendar_MultipleNonOverlappingWindows asserts binary search
// picks the right window when several disjoint windows exist.
func TestCalendar_MultipleNonOverlappingWindows(t *testing.T) {
	c := NewCalendar([]SuppressionWindow{
		{Start: utc(2), End: utc(3), Reason: "morning"},
		{Start: utc(10), End: utc(11), Reason: "midday"},
		{Start: utc(20), End: utc(21), Reason: "evening"},
	})

	cases := []struct {
		name       string
		t          time.Time
		want       bool
		wantReason string
	}{
		{"in_morning", utc(2).Add(30 * time.Minute), true, "morning"},
		{"between_morning_midday", utc(5), false, ""},
		{"in_midday", utc(10).Add(30 * time.Minute), true, "midday"},
		{"between_midday_evening", utc(15), false, ""},
		{"in_evening", utc(20).Add(30 * time.Minute), true, "evening"},
		{"after_all", utc(23), false, ""},
		{"before_all", utc(0), false, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, reason := c.Suppresses(tc.t)
			if got != tc.want {
				t.Errorf("Suppresses = %v; want %v", got, tc.want)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q; want %q", reason, tc.wantReason)
			}
		})
	}
}

// TestCalendar_OverlappingWindowsLinearWalk asserts the linear-walk
// fallback finds an earlier-start window whose End extends past a
// later-start window.
//
// Layout:
//
//	W0: [02:00, 23:00) "long_maintenance"
//	W1: [10:00, 11:00) "midday"
//
// At t=22:30: W1's Start is the rightmost ≤ t, but W1.End=11:00 ≤ t.
// The detector must walk back to W0 and find it covers t.
func TestCalendar_OverlappingWindowsLinearWalk(t *testing.T) {
	c := NewCalendar([]SuppressionWindow{
		{Start: utc(2), End: utc(23), Reason: "long_maintenance"},
		{Start: utc(10), End: utc(11), Reason: "midday"},
	})
	got, reason := c.Suppresses(utc(22).Add(30 * time.Minute))
	if !got {
		t.Error("Suppresses at 22:30 = false; want true (W0 covers it)")
	}
	if reason != "long_maintenance" {
		t.Errorf("reason = %q; want %q", reason, "long_maintenance")
	}
}

// TestCalendar_ReturnsReasonOnHit asserts the Reason string round-trips.
func TestCalendar_ReturnsReasonOnHit(t *testing.T) {
	c := NewCalendar([]SuppressionWindow{
		{Start: utc(10), End: utc(11), Reason: "Q3 holiday freeze"},
	})
	got, reason := c.Suppresses(utc(10).Add(30 * time.Minute))
	if !got {
		t.Fatal("expected suppression hit")
	}
	if reason != "Q3 holiday freeze" {
		t.Errorf("reason = %q; want %q", reason, "Q3 holiday freeze")
	}
}

// TestCalendar_NewCalendarCopiesInput asserts NewCalendar takes a
// defensive copy — mutating the caller's slice afterwards must not
// affect the Calendar.
func TestCalendar_NewCalendarCopiesInput(t *testing.T) {
	in := []SuppressionWindow{
		{Start: utc(10), End: utc(11), Reason: "real"},
	}
	c := NewCalendar(in)
	// Mutate the original; the calendar should be unaffected.
	in[0].Reason = "tampered"
	in[0].Start = utc(0)
	in[0].End = utc(1)

	got, reason := c.Suppresses(utc(10).Add(30 * time.Minute))
	if !got {
		t.Fatal("expected hit at 10:30 against original window")
	}
	if reason != "real" {
		t.Errorf("reason = %q; want %q (input mutation leaked)", reason, "real")
	}
}
