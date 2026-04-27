# Anomaly Detection

> Status: implemented in `pkg/anomaly` (v0.0.8). Test corpus:
> `pkg/anomaly/*_test.go` — synthetic-injection tests against
> deterministic seasonal series.

## Problem statement

NetSite ingests time-series signals — canary success rates, latency
percentiles, BGP update counts, flow throughput — and an operator
needs to know "is the latest value abnormal?" The operator's working
definition of "abnormal" is not "more than three standard deviations
away from the mean of the last hour." That definition fires every
Saturday morning when traffic naturally drops, and stays silent during
real outages whose magnitude is comparable to the seasonal swing.

What the operator actually wants:

1. A model that knows the series has a **daily** (and often **weekly**)
   shape, so the 03:00 dip on a Sunday is not flagged as a regression.
2. A residual measured against that shape — how far is the current
   point from where the model expects it to be?
3. A scale-invariant threshold — "5 MAD over the residual fit" reads
   the same way for a series with values near 1 (success rate) as for
   one with values near 1e6 (packet count).
4. A way to **silence** known windows (scheduled maintenance, holiday
   freeze) without turning the math off — the model should keep
   learning, and the dashboard should keep showing the residual, but
   the alerter should stay quiet.
5. Output that explains itself. When an alert fires, the operator
   wants to know which model ran, what the forecast was, how big the
   residual was in MAD units, and whether a calendar window applied.

The naive z-score approach fails on every one of those requirements.

## Why naive approaches fail

**Z-score over a rolling window.** A rolling window mixes the
nighttime baseline with the daytime baseline, inflating the standard
deviation and blunting detection. Worse, it fires every time the
series enters a known seasonal trough.

**Static thresholds ("alert if success rate < 99 %").** Static
thresholds are unmaintainable across SLOs and require a human to
re-tune them per service. They also miss slow drift — a service
moving from 99.95 % to 99.55 % is the most actionable signal you can
get; a static threshold at 99 % silently ignores it.

**Pure trend extrapolation (linear regression).** Captures drift but
not seasonality. Flags every Saturday.

**One-shot ML (autoencoder, isolation forest, Prophet).** All viable,
but they're opaque. When the alert fires at 03:14 the operator wants
a one-line explanation, not a SHAP summary. Any model we ship has to
produce a residual we can explain to a human in a sentence.

## What we ship in v0.0.8

Two methods, chosen by data density, both producing a one-step
forecast and a residual on the same scale (MAD of the in-sample fit).

### 1. Holt-Winters additive triple-exponential smoothing

`pkg/anomaly/holtwinters.go`. Maintains three exponentially-weighted
state components — level, trend, season — and produces a one-step
forecast that respects the seasonal shape.

The recursion (additive form):

```
level_t  = α (y_t − season_{t−period}) + (1 − α)(level_{t−1} + trend_{t−1})
trend_t  = β (level_t − level_{t−1})    + (1 − β)  trend_{t−1}
season_t = γ (y_t − level_t)            + (1 − γ)  season_{t−period}

forecast_{t+h} = level_t + h · trend_t + season_{t+h−period}
```

Defaults: α = 0.3, β = 0.1, γ = 0.1. Period is operator-supplied
(default 24 — daily seasonality on hourly samples). Initialisation:

- Level: mean of the first period.
- Trend: averaged pairwise slope across the first two periods.
- Seasonals: per-phase mean across the first two cycles, recentred
  to sum to zero (the additive invariant).

Holt-Winters is the right answer when the series has a clear cycle
*and* drifts. The trend term tracks the drift; the season term tracks
the cycle. The level term absorbs everything else.

**Why additive, not multiplicative.** Success-rate SLIs live in
[0, 1]. Multiplicative seasonality misbehaves near zero — a small
multiplicative season hits a near-zero level and produces meaningless
forecasts. Additive is well-defined across the whole range. The
tradeoff: if seasonality scales with level (flow rate where weekday
is 10× weekend), multiplicative wins. We add the multiplicative
variant when a real series demands it.

References:

- Hyndman & Athanasopoulos, *Forecasting: Principles and Practice*,
  3rd ed., chapter 8 — derivation, parameter discussion, and tradeoffs.
- Holt (1957) and Winters (1960), the original papers — formulation
  in additive and multiplicative form.

### 2. Classical seasonal decomposition (simplified STL)

`pkg/anomaly/stl.go`. Models `y_t = trend_t + season_t + residual_t`
in three passes:

1. Trend: centred moving average of length `period`. For even periods
   we use the standard 2 × period MA so the trend lands centred
   between observations rather than offset to one side.
2. Season: per-phase mean of `(y − trend)`, recentred to sum to zero.
3. Residual: `y − trend − season`. Edges (the first and last
   `period/2` points where the centred MA is undefined) are left at
   zero; downstream MAD computation skips them.

**Why a simplified STL, not full STL-LOESS.** The canonical algorithm
(Cleveland, Cleveland, McRae & Terpenning 1990) replaces the moving
average and the per-phase average with two iterated LOESS smoothers.
LOESS is locally-weighted regression with bandwidth and degree
parameters; correctly implementing it against published reference
data is roughly 500 lines of careful numerics. Classical
decomposition (moving average for trend, period averages for season,
residuals from both) gets us 80 % of the operational value at 10 %
of the implementation cost.

**Where this simplification is good enough:**

- Series with a single, stable seasonal period (daily for hourly
  data, weekly for daily data).
- Series where edge effects are tolerable.

**Where it is not:**

- Series whose seasonal *shape* drifts over time. Full STL
  re-estimates the season at every iteration; we estimate it once.
- Sub-daily series with multiple overlapping periods (e.g.,
  daily + weekly together). Run the detector against one period at a
  time for now.

Full STL arrives in Phase 1 once we have enough real-world series to
calibrate LOESS bandwidth and verify against an independent reference
implementation.

References:

- Cleveland, Cleveland, McRae & Terpenning, "STL: A
  Seasonal-Trend Decomposition Procedure Based on Loess",
  *Journal of Official Statistics* 6 (1990): 3–73.

### Method chooser

`chooseMethod` (in `pkg/anomaly/detector.go`) picks one method per
call:

```
n < MinSamples            → InsufficientData
n < 2 · period            → InsufficientData
2 · period ≤ n < 4 · period → HoltWinters
n ≥ 4 · period            → SeasonalDecompose
```

The 4-cycle threshold for STL is a tuning choice. With three or fewer
cycles the per-phase mean is too noisy; with four or more it is
stable enough to surface real residuals.

**Why we pick one method per call rather than ensembling.**
Ensembling adds a free parameter (how to combine the two outputs)
that we'd need calibration data to tune. The Verdict is also easier
to explain when it carries one method, one residual, one threshold.
Phase 1 will revisit when we have labelled production data.

## Severity classification

The latest residual is divided by the MAD (median absolute deviation)
of the in-sample residuals. The result, `MADUnits`, is the
scale-invariant distance from expectation. Operators tune the
breakpoints; defaults follow the standard SRE practice of widening
bands as suspicion grows.

| MADUnits      | Severity   | Meaning                              |
|---------------|------------|--------------------------------------|
| < 3           | none       | within normal band                   |
| ≥ 3 and < 5   | watch      | unusual; surface, do not page        |
| ≥ 5 and < 8   | anomaly    | likely abnormal; page                |
| ≥ 8           | critical   | clearly abnormal; page urgently      |

NaN MAD (constant series) → SeverityNone.

**Why MAD rather than standard deviation.** MAD ignores the largest
deviations by construction (1.4826 · MAD ≈ σ for normal data, and
unlike σ it tolerates a small fraction of outliers without
inflating). A real outage in the recent fit window does not blow up
the residual scale and silence subsequent alerts.

## Calendar suppression

`pkg/anomaly/calendar.go`. Operators flag UTC half-open intervals
`[Start, End)` during which alerts are silenced — scheduled
maintenance, weekend quiet periods, holiday freezes. The detector
still computes the residual and severity (so dashboards remain
accurate); it reports `Suppressed = true` and caps Severity at
`watch` for the Verdict.

**Why suppress rather than skip evaluation.** Keeping the math
running during suppression windows means the seasonal model continues
to learn, and the dashboard continues to show the residual so a human
can glance and confirm the suppression was justified. Skipping the
math entirely would silently distort the model.

**Why a list of windows, not a recurrence rule.** Recurring patterns
(weekends, Mondays, business hours) cover the common case and should
be a Phase 1 add. For v0.0.8 we ship the discrete-window form because
the data structure round-trips through Postgres JSON cleanly and
dashboards render each window as a single time-axis band.

`Calendar.Suppresses(t)` is a binary search across windows sorted by
`Start`, with a small linear walk back to handle the rare case of
overlapping windows (one long maintenance covering a small midday
window).

## Verdict — the explainable output

```go
type Verdict struct {
    Method      Method    // holt_winters | seasonal_decompose | insufficient_data
    Severity    Severity  // none | watch | anomaly | critical
    Suppressed  bool
    LatestPoint Point
    Forecast    float64   // model's expectation for the latest point
    Residual    float64   // LatestPoint.Value − Forecast
    MAD         float64   // median absolute deviation of fit residuals
    MADUnits    float64   // |Residual| / MAD
    Reason      string    // free-text explanation aimed at humans
    EvaluatedAt time.Time // server time at evaluation, UTC
}
```

Every field exists to make the decision auditable. When an operator
asks "why did this fire?" the Verdict says which method ran, what
the forecast was, how large the residual was in MAD units, and
whether a calendar window applied. The `Reason` string spells the
state out: e.g., `Holt-Winters: level=0.9923 trend=-0.0001
season[14]=0.0123` for an HW-driven verdict.

## Calibration methodology

For v0.0.8 calibration is **synthetic-only**: deterministic seasonal
series with controlled noise and an injected spike at the latest
point. Tests verify:

- Empty / unsorted / too-short input is rejected with the right
  sentinel error.
- A clean (zero-noise) seasonal series produces SeverityNone or
  SeverityWatch.
- A 5-unit injection at the last point produces SeverityCritical.
- A 5-cycle series triggers the seasonal-decompose branch; a
  shorter one stays on Holt-Winters.
- A calendar window covering the last point caps Severity at
  watch even when the math says critical.

Real-data calibration arrives in Phase 1 once we have canary results
flowing from at least three POPs over a multi-week window. The plan:

- Build a labelled corpus of historical canary series with operator
  annotations of "real anomaly" vs. "false positive".
- Compute ROC over the MAD threshold knobs and pick a per-SLI default.
- Track `madUnits` distributions per series in production; investigate
  any series whose 95th percentile sits above 3 (suggests the model
  is too tight or the series too noisy).

## Failure modes and known limitations

- **Initialisation transient.** Holt-Winters' seed values come from
  the first `2 · period` points; their residuals are zero by
  construction (the seed window mirrors the input). MAD computation
  drops the seed window. If the seed window happens to span an
  outage, the trend term will start biased. This usually self-corrects
  within `~1 / β` cycles (~10 with β = 0.1).
- **Edge effects in classical decomposition.** Trend is undefined for
  the first and last `period/2` points; the detector substitutes the
  last well-defined trend value when computing the forecast at the
  right edge. This is biased toward the last interior trend value
  rather than toward an extrapolation. Acceptable in practice — the
  detector cares about the right edge and the bias is small unless
  the series is changing fast at exactly that moment, in which case
  the residual will reflect the change anyway.
- **Drifting seasonal shape.** Both methods assume the shape of one
  cycle is a small perturbation of the next. A series whose Sunday
  shape gradually drifts away from its Monday shape will see slowly
  rising residuals — the fit is "right on average" but the per-phase
  forecast keeps lagging. Full STL handles this; the simplified form
  does not.
- **Sub-period spikes that get smoothed.** A spike of duration 1
  inside a centred MA of span `period` is attenuated by `1/period`.
  Detection survives because we work with the residual, not the
  smoothed value, but operators reading `Trend` directly should know
  the trend curve under-reacts to short spikes.
- **Constant series.** MAD of a constant series is zero, MADUnits is
  defined as 0 (we guard against division by zero), Severity is
  None. The detector cannot distinguish "stable healthy" from
  "stuck" — that's the SLO engine's job, not the anomaly detector's.

## What changes in Phase 1+

- Full STL-LOESS implementation, calibrated against statsmodels'
  reference output on a frozen corpus.
- Recurrence rules for calendar suppression (weekly, monthly).
- Multiplicative Holt-Winters for series whose seasonality scales
  with level.
- Adaptive period detection (autocorrelation-based) so operators
  don't need to specify Period when it isn't obvious.
- Optional ensemble across both methods with a learned weight, after
  we have a labelled corpus from production.
