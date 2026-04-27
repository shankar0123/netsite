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
	"time"
)

// What: calendar suppression. Operators flag windows during which
// the detector should NOT fire — scheduled maintenance, weekend
// quiet periods, holiday weeks. The detector still computes the
// residual and severity (so dashboards remain accurate); it just
// reports Suppressed=true and never crosses the SeverityAnomaly
// threshold for the Verdict.
//
// How: a SuppressionWindow is a half-open interval [Start, End)
// in UTC. Calendar.Suppresses(t) is a binary search across sorted
// windows. The detector stuffs the configured Calendar into Config
// and runs Suppresses against the latest point's timestamp.
//
// Why we suppress rather than skip evaluation: keeping the math
// running during suppression windows means the seasonal model
// continues to learn (the data is real, even if alerts are
// silenced), and the dashboard shows the residual so a human can
// glance and confirm the suppression was justified. Skipping the
// math entirely would silently distort the model.
//
// Why a SuppressionWindow list (not a recurrence rule):
// recurring patterns (weekends, Mondays, business hours) cover the
// common case and should be a Phase 1 add. For v0.0.8 we ship the
// simpler discrete-window form because the data structure round-
// trips through Postgres JSON cleanly and dashboards can render
// each window as a single time-axis band.

// SuppressionWindow marks a UTC interval during which alerts are
// silenced. Reason is operator-supplied free text ("monthly
// maintenance", "Q3 holiday freeze") that surfaces in the Verdict.
type SuppressionWindow struct {
	Start  time.Time
	End    time.Time
	Reason string
}

// Calendar wraps a list of SuppressionWindows with binary-searchable
// lookup. Construct via NewCalendar; Suppresses is the only operation.
type Calendar struct {
	windows []SuppressionWindow
}

// NewCalendar returns a Calendar with windows sorted by Start, ready
// for Suppresses lookups.
//
// Why we sort once at construction: a Detector evaluates many
// points against the same Calendar; pre-sorting keeps each
// Suppresses call O(log n) instead of O(n log n).
func NewCalendar(windows []SuppressionWindow) Calendar {
	dup := append([]SuppressionWindow(nil), windows...)
	sort.Slice(dup, func(i, j int) bool {
		return dup[i].Start.Before(dup[j].Start)
	})
	return Calendar{windows: dup}
}

// Suppresses reports whether t falls inside any window.
// half-open semantics: a point exactly at End is NOT suppressed;
// a point exactly at Start IS suppressed.
//
// Behaviour for empty Calendar: returns (false, "").
func (c Calendar) Suppresses(t time.Time) (bool, string) {
	if len(c.windows) == 0 {
		return false, ""
	}
	// Binary search: find the rightmost window whose Start <= t.
	idx := sort.Search(len(c.windows), func(i int) bool {
		return c.windows[i].Start.After(t)
	})
	// idx points one past the last window whose Start <= t.
	if idx == 0 {
		return false, ""
	}
	w := c.windows[idx-1]
	if t.Before(w.End) {
		return true, w.Reason
	}
	// Possibly an overlapping earlier window — e.g., a long
	// maintenance window (idx-2) that fully contains a smaller
	// midday window (idx-1). Walk back and return the first ancestor
	// whose End extends past t. Windows are sorted by Start, but
	// their Ends are not; we cannot short-circuit on a single
	// non-covering ancestor. Real-world windows rarely overlap, so
	// this loop is normally just one or two iterations.
	for j := idx - 2; j >= 0; j-- {
		if t.Before(c.windows[j].End) {
			return true, c.windows[j].Reason
		}
	}
	return false, ""
}

// Len returns the number of windows. Useful for tests and debug.
func (c Calendar) Len() int { return len(c.windows) }
