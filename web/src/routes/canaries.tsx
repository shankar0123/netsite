// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import {
  ApiError,
  getAnomalyForTest,
  listTests,
  type AnomalyVerdict,
  type Test,
} from "../api/client";
import { execute, type NetQLExecuteResponse } from "../api/netql";
import { LineChart } from "../components/Chart";
import { useEffect, useState } from "react";

// What: /canaries list view + per-canary detail.
//
// List: cards with kind / target / interval. Click → detail.
// Detail: three sections per canary —
//   1. Anomaly verdict card (v0.0.22): the most recent verdict
//      cached by the controlplane evaluator goroutine — severity
//      pill, residual, MAD units, reason, "evaluated N minutes ago".
//      404 from the API renders as "no verdict cached yet" because
//      the evaluator hasn't ticked since this test was created.
//   2. Latency-by-pop chart (v0.0.18): netql query for
//      `latency_p95 by pop where pop = '...' over 24h`. Phase 0
//      doesn't have `observed_at` as a netql group-by column yet,
//      so this falls back to per-POP comparison until v0.0.23
//      lands the time-bucket dimension.
//   3. Errors-by-kind list (v0.0.18): netql `count by error_kind
//      over 24h`. Failure-mode breakdown.
//
// The "annotation markers on the timeline" half of the exit-gate
// "annotation pinned to canary timestamp surfaces in canary detail"
// criterion lands once the netql metric set gains an
// `observed_at`-by group dimension; for now the annotations list
// at /annotations (filtered by scope=canary) proves the data path.

export function CanariesPage() {
  const q = useQuery({ queryKey: ["tests"], queryFn: listTests });
  if (q.isPending) return <p className="text-sm text-zinc-500">Loading…</p>;
  if (q.isError) return <p className="text-sm text-red-400">Failed to load tests.</p>;
  if (q.data.length === 0)
    return (
      <p className="text-sm text-zinc-500">
        No tests yet. POST one to <code className="font-mono">/v1/tests</code>.
      </p>
    );

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Canaries</h1>
        <p className="text-sm text-zinc-400">
          {q.data.length} test{q.data.length === 1 ? "" : "s"} in this tenant.
        </p>
      </header>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {q.data.map((t) => (
          <CanaryCard key={t.id} test={t} />
        ))}
      </div>
    </div>
  );
}

function CanaryCard({ test }: { test: Test }) {
  return (
    <Link
      to="/canaries/$id"
      params={{ id: test.id }}
      className="rounded-lg border border-zinc-800 bg-zinc-900/50 hover:bg-zinc-900 p-4 transition block"
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="font-mono text-xs text-zinc-300 truncate">{test.id}</span>
        <span className="text-xs text-zinc-500">{test.kind}</span>
      </div>
      <p className="mt-1 text-sm text-zinc-100 truncate">{test.target}</p>
      <p className="mt-2 text-xs text-zinc-500">
        every {Math.round(test.interval_ms / 1000)}s · timeout{" "}
        {Math.round(test.timeout_ms / 1000)}s · {test.enabled ? "enabled" : "disabled"}
      </p>
    </Link>
  );
}

// CanaryDetailPage shows three things for one test:
//   - The latest anomaly verdict cached by the controlplane evaluator.
//   - latency_p95 by pop over 24h    (per-POP latency comparison).
//   - count by error_kind over 24h   (failure-mode breakdown).
// Charts go through netql so the SQL is auditable from /netql; the
// verdict comes straight from /v1/anomaly/tests/{id}.
export function CanaryDetailPage() {
  const { id } = useParams({ from: "/canaries/$id" });
  const [latency, setLatency] = useState<NetQLExecuteResponse | null>(null);
  const [errors, setErrors] = useState<NetQLExecuteResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // Anomaly verdict is its own TanStack Query so it can refresh
  // independently (e.g. via window-focus refetch) without touching
  // the chart fetches. retry: false so a 404 ("no verdict cached
  // yet") renders the empty state immediately rather than retrying.
  const anomalyQ = useQuery({
    queryKey: ["anomaly", id],
    queryFn: () => getAnomalyForTest(id),
    retry: false,
  });

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    Promise.all([
      execute(
        `latency_p95 by pop where pop = 'pop-dev-local' over 24h`, // placeholder; v0.0.23 uses test_id when netql adds it as a filter column
      ).catch((e) => {
        // Swallow individual chart errors — show what we can.
        if (!cancelled) setErr(String(e));
        return null as unknown as NetQLExecuteResponse;
      }),
      execute(`count by error_kind over 24h`).catch((e) => {
        if (!cancelled) setErr(String(e));
        return null as unknown as NetQLExecuteResponse;
      }),
    ]).then(([l, e]) => {
      if (cancelled) return;
      setLatency(l);
      setErrors(e);
    });
    return () => {
      cancelled = true;
    };
  }, [id]);

  return (
    <div className="space-y-6">
      <header>
        <Link to="/canaries" className="text-xs text-sky-400 hover:text-sky-300">
          ← all canaries
        </Link>
        <h1 className="text-2xl font-semibold mt-2">
          <span className="font-mono text-base">{id}</span>
        </h1>
      </header>

      <AnomalySection q={anomalyQ} />

      {err ? <pre className="text-xs text-red-400 whitespace-pre-wrap">{err}</pre> : null}

      <section className="space-y-2">
        <h2 className="text-sm font-medium text-zinc-200">p95 latency by POP (24h)</h2>
        <div className="rounded-md bg-zinc-900/50 border border-zinc-800 p-4">
          {latency ? (
            <LineChart columns={latency.columns} rows={latency.rows} />
          ) : (
            <p className="text-xs text-zinc-500 italic">Loading…</p>
          )}
        </div>
      </section>

      <section className="space-y-2">
        <h2 className="text-sm font-medium text-zinc-200">Errors by kind (24h)</h2>
        <div className="rounded-md bg-zinc-900/50 border border-zinc-800 p-4">
          {errors && errors.rows.length > 0 ? (
            <ul className="text-sm space-y-1">
              {errors.rows.map((r, i) => (
                <li key={i} className="flex justify-between">
                  <span className="font-mono text-xs">{String(r[0] ?? "(none)")}</span>
                  <span className="text-zinc-400">{String(r[r.length - 1])}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-zinc-500 italic">No errors in the window.</p>
          )}
        </div>
      </section>
    </div>
  );
}

// AnomalySection renders the latest /v1/anomaly/tests/{id} verdict.
//
// Three states:
//   - Loading: pending TanStack Query.
//   - 404 ("no verdict cached yet"): renders the empty-state copy.
//     This is the common case when a test was just created and the
//     5-minute evaluator tick hasn't reached it.
//   - Other errors: renders an inline red banner with the message.
//   - Success: severity pill + method + a small grid of the
//     numeric fields (last value, forecast, residual, MAD, MAD
//     units), the human-readable reason, and "evaluated N min ago".
//
// Why we render every field rather than just the severity pill: an
// operator looking at a "anomaly" badge wants to know WHY. The
// reason string is the prose answer, but the numbers are what they
// page in to validate the reason isn't lying. Hiding them would
// force a curl call.
function AnomalySection({
  q,
}: {
  q: { isPending: boolean; isError: boolean; error: unknown; data?: AnomalyVerdict };
}) {
  return (
    <section className="space-y-2">
      <h2 className="text-sm font-medium text-zinc-200">
        Anomaly detector — latest verdict
      </h2>
      <div className="rounded-md bg-zinc-900/50 border border-zinc-800 p-4">
        {q.isPending ? (
          <p className="text-xs text-zinc-500 italic">Loading…</p>
        ) : q.isError ? (
          <AnomalyError err={q.error} />
        ) : q.data ? (
          <AnomalyVerdictBody v={q.data} />
        ) : (
          <p className="text-xs text-zinc-500">No data.</p>
        )}
      </div>
    </section>
  );
}

function AnomalyError({ err }: { err: unknown }) {
  if (err instanceof ApiError && err.status === 404) {
    return (
      <p className="text-xs text-zinc-500">
        No anomaly verdict cached yet. The evaluator ticks every 5
        minutes (override via{" "}
        <code className="font-mono">NETSITE_ANOMALY_INTERVAL</code>);
        check back shortly.
      </p>
    );
  }
  const msg =
    err instanceof ApiError
      ? `${err.status}: ${err.body || "(no body)"}`
      : String(err);
  return (
    <p className="text-xs text-red-400" role="alert">
      Failed to load anomaly verdict — {msg}
    </p>
  );
}

const SEV_CLASSES: Record<AnomalyVerdict["severity"], string> = {
  none: "bg-emerald-900/50 text-emerald-300",
  watch: "bg-amber-900/50 text-amber-300",
  anomaly: "bg-orange-900/50 text-orange-300",
  critical: "bg-red-900/50 text-red-300",
};

function AnomalyVerdictBody({ v }: { v: AnomalyVerdict }) {
  const evaluatedAgo = relativeAgo(v.evaluated_at);
  const sevClass = SEV_CLASSES[v.severity] ?? "bg-zinc-800 text-zinc-300";
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-baseline gap-2">
        <span
          className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${sevClass}`}
        >
          {v.severity}
        </span>
        <span className="font-mono text-xs text-zinc-400">{v.method}</span>
        <span className="font-mono text-xs text-zinc-500">
          metric={v.metric}
        </span>
        {v.suppressed ? (
          <span className="inline-block rounded bg-zinc-800 px-1.5 py-0.5 text-[10px] font-medium text-zinc-300">
            calendar-suppressed
          </span>
        ) : null}
        <span className="ml-auto text-[10px] text-zinc-500 font-mono">
          evaluated {evaluatedAgo}
        </span>
      </div>

      {v.reason ? (
        <p className="text-sm text-zinc-300">{v.reason}</p>
      ) : null}

      <div className="grid grid-cols-2 sm:grid-cols-5 gap-2 text-xs text-zinc-400">
        <Stat label="last_value" value={v.last_value.toFixed(3)} />
        <Stat label="forecast" value={v.forecast.toFixed(3)} />
        <Stat label="residual" value={v.residual.toFixed(3)} />
        <Stat label="MAD" value={v.mad.toFixed(3)} />
        <Stat label="MAD units" value={v.mad_units.toFixed(2)} />
      </div>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-zinc-500">
        {label}
      </div>
      <div className="font-mono text-sm text-zinc-200">{value}</div>
    </div>
  );
}

// relativeAgo returns "N min ago" / "N hr ago" for an ISO timestamp.
// Bounded at "just now" for sub-30s deltas; we don't bother with
// "in the future" because the server's clock can drift forward of
// the client's by seconds and we'd render a confusing negative.
function relativeAgo(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const deltaSec = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (deltaSec < 30) return "just now";
  if (deltaSec < 60) return `${deltaSec}s ago`;
  if (deltaSec < 3600) return `${Math.round(deltaSec / 60)} min ago`;
  if (deltaSec < 86400) return `${Math.round(deltaSec / 3600)} hr ago`;
  return `${Math.round(deltaSec / 86400)} d ago`;
}
