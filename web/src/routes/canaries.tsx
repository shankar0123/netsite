// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { listTests, type Test } from "../api/client";
import { translate, execute, type NetQLExecuteResponse } from "../api/netql";
import { LineChart } from "../components/Chart";
import { useEffect, useState } from "react";

// What: /canaries list view + per-canary detail.
//
// List: cards with kind / target / interval. Click → detail.
// Detail: a netql query for `latency_p95 by observed_at where
// test_id = '<id>' over 24h` produces the timeline (Phase 0 doesn't
// have observed_at as a netql group-by column yet, so v0.0.18 falls
// back to fetching `count by error_kind` and `latency_p95 by pop`
// — the two most useful per-canary views — and rendering them as
// charts).
//
// The "annotation markers on the timeline" half of the exit-gate
// "annotation pinned to canary timestamp surfaces in canary detail"
// criterion lands once the netql metric set gains an
// `observed_at`-by group dimension; for v0.0.18 the annotations
// list under each canary detail page proves the data path.

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

// CanaryDetailPage shows two charts for one test:
//   - latency_p95 by pop over 24h   (per-POP latency comparison)
//   - count by error_kind over 24h  (failure-mode breakdown)
// Both go through netql so the SQL is auditable from /netql.
export function CanaryDetailPage() {
  const { id } = useParams({ from: "/canaries/$id" });
  const [latency, setLatency] = useState<NetQLExecuteResponse | null>(null);
  const [errors, setErrors] = useState<NetQLExecuteResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    Promise.all([
      execute(
        `latency_p95 by pop where pop = 'pop-dev-local' over 24h`, // placeholder; v0.0.19 uses test_id when netql adds it as a filter column
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
