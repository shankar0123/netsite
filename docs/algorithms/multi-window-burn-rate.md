# Multi-window multi-burn-rate SLO alerting

> Source: [`pkg/slo/evaluator.go`](../../pkg/slo/evaluator.go).
> Tests:  [`pkg/slo/evaluator_test.go`](../../pkg/slo/evaluator_test.go).
> Reference: Google SRE Workbook ch. 5 ("Alerting on SLOs"),
> <https://sre.google/workbook/alerting-on-slos/>.

## What this algorithm does

Given an SLO (e.g. "the canary against api.example.com should
succeed 99.9 % of the time over a 30-day window"), classify the SLO
right now into one of four states:

- **ok** — burn rate is below all thresholds.
- **slow_burn** — both the 6-hour long window and the 30-minute short
  window show burn rate ≥ 6×.
- **fast_burn** — both the 1-hour long window and the 5-minute short
  window show burn rate ≥ 14.4×.
- **no_data** — the underlying canary_results table held no rows in
  either fast window. The SLO has gone silent; the operator wants to
  know.

A transition into `slow_burn` or `fast_burn` fires the configured
notifier (webhook, log, future PagerDuty/Slack). Transitions back to
ok do not fire today; recovery webhooks land in Phase 1.

## The two key ideas

### Burn rate, not raw breach

Define `burnRate = (1 - SLI) / (1 - objective)`. A burn rate of 1×
means we're consuming the error budget exactly as fast as it accrues.
A burn rate of 14.4× means we're consuming it 14.4× faster than the
budget refills — at that pace, **the entire 30-day budget burns in
2 hours**. That's a clean operational threshold: anything below 14.4×
in a fast-burn window means we'd have to sustain it for hours to
matter; anything above means we have a real problem within the hour.

Single-threshold "alert when SLI < target" is the alternative, and it
fails one of two ways. Either you alert on any breach (noisy: every
brief spike pages someone) or you alert when the budget is busted
(slow: you wake up to "we already burned the month's budget at 3am").
Burn rate gives you a knob between the two extremes.

### Multi-window confirmation kills false positives

If you alert on `burnRate(1h) > 14.4`, you'll false-positive every
time a 5-minute spike happens to land inside the 1-hour window. The
fix: require **both** a long window **and** a short window to exceed
the threshold. The long window establishes that the burn is
sustained; the short window establishes that it's still happening
right now. Either alone is unreliable.

For NetSite the canonical pairs are:

| Severity | Long window | Short window | Threshold | Time-to-alert | Budget burned at threshold |
|---|---|---|---|---|---|
| fast_burn | 1 h | 5 m | 14.4 × | ≈ 5 min | 2 % per hour |
| slow_burn | 6 h | 30 m | 6.0 × | ≈ 30 min | 5 % per 6 h |

These are the SRE Workbook defaults, derived from a 30-day window and
a 99.9 % objective — operators can override per-SLO if their window
or objective differs.

## Worked example

An SLO with objective 99.9 % observes:

- 1-hour SLI = 99.0 %  →  burn = (1 − 0.99)/(1 − 0.999) = 10×
- 5-minute SLI = 99.0 %  →  burn = 10×
- 6-hour SLI = 99.5 %  →  burn = 5×
- 30-minute SLI = 99.5 %  →  burn = 5×

10× < 14.4× so **fast_burn** does not fire. 5× < 6× so **slow_burn**
does not fire either. Status: **ok**, despite a measurable
degradation.

If the 1-hour SLI drops to 98.0 %:

- 1-hour SLI = 98.0 %  →  burn = 20× ✓ above 14.4×
- 5-minute SLI = 98.0 %  →  burn = 20× ✓ above 14.4×

→ **fast_burn** fires.

## Why we picked these defaults

The 1h+5m / 6h+30m pairs maintain a useful property: roughly the same
fraction of error budget is at stake when each fires.

- fast_burn at 14.4× over 1h ⇒ 2 % of the 30-day budget burned in 1h.
- slow_burn at 6× over 6h ⇒ 5 % of the 30-day budget burned in 6h.

Operators who care about a specific budget percentage (rather than
time-to-alert) can derive their own thresholds from the SRE Workbook
table; the math is purely the algebra of `burnRate × window /
(window_total × (1 - objective))`. The CLI doesn't surface this
calculator yet — Phase 1 task.

## What we deliberately do NOT do (yet)

- **Tickets vs. pages.** SRE Workbook also recommends a third
  severity ("warn / ticket") at lower burn rates. We omit it to keep
  v0.0.7's surface tight; the existing `slow_burn` covers most
  ticket-level escalations.
- **Recovery alerts.** A burn → ok transition does not page. Adding
  a recovery channel adds rule complexity without clear operator
  demand. Reconsider in Phase 1.
- **Per-SLI per-tenant calibration.** Every SLO uses the same default
  thresholds unless the operator overrides at create time. A tenant-
  wide override would land in Phase 5 alongside multi-tenant config.
- **Time-anchored windows.** We compute SLIs over trailing time
  windows (`observed_at >= now() - INTERVAL N SECOND`). Aligned
  windows (calendar hour, calendar day) would give nicer dashboard
  ticks but add complexity around boundary effects. Trailing wins for
  alerting; calendar windows can be added for reporting later.

## Failure modes worth flagging

- **No data is silent failure.** If POPs stop publishing,
  canary_results gets no rows and trailing-window SLIs are 100 %.
  We special-case this with `StatusNoData` and report it; operators
  should still wire a separate "is the POP healthy" alert (the
  controlplane health check counts).
- **Tiny windows produce wild burn rates.** A 5-minute window over a
  test that fires every 30 s holds at most 10 rows; a single failure
  spikes the burn rate to 10×. The multi-window rule keeps us from
  alerting on this alone, but operators looking at single-window
  graphs should know what they're seeing.
- **Threshold drift.** Operators who change their objective without
  updating thresholds get inconsistent alerts. Document defaults
  clearly; consider a "reset thresholds to default" admin action when
  the UI ships.

## Prior art

- Google SRE Workbook ch. 5 — original write-up of the algorithm.
- Cloudflare blog: "How we measure customer-facing SLAs"
  (multi-window in production).
- GitHub blog: "How GitHub uses multi-burn-rate alerts"
  (operational lessons around threshold tuning).
- Prometheus mixins: <https://github.com/kubernetes-monitoring/kubernetes-mixin>
  ships PromQL recording rules for the same math.
