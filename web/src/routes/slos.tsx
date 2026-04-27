// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { listSLOs, type SLO } from "../api/client";

// What: /slos list view. One row per SLO with name + objective +
// SLI kind + window + filter summary + enabled badge.
//
// Why no burn-rate chart yet: the SLO `state` row (last burn-rate,
// last status) lives in Postgres and isn't exposed on the LIST
// endpoint today (see pkg/slo/store.go — list returns SLO defs
// only, not slo_state). v0.0.19 either (a) joins the state row
// into the list response or (b) adds a /v1/slos/{id}/state
// endpoint and the chart fetches per-SLO.

export function SLOsPage() {
  const q = useQuery({ queryKey: ["slos"], queryFn: listSLOs });
  if (q.isPending) return <p className="text-sm text-zinc-500">Loading…</p>;
  if (q.isError) return <p className="text-sm text-red-400">Failed to load SLOs.</p>;
  if (q.data.length === 0)
    return (
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold">SLOs</h1>
        <p className="text-sm text-zinc-500">
          No SLOs yet. POST one to <code className="font-mono">/v1/slos</code>.
        </p>
      </div>
    );
  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">SLOs</h1>
        <p className="text-sm text-zinc-400">
          {q.data.length} SLO{q.data.length === 1 ? "" : "s"} in this tenant.
        </p>
      </header>
      <div className="space-y-3">
        {q.data.map((s) => (
          <SLORow key={s.id} slo={s} />
        ))}
      </div>
    </div>
  );
}

function SLORow({ slo }: { slo: SLO }) {
  const days = Math.round(slo.window_seconds / 86400);
  const obj = (slo.objective_pct * 100).toFixed(slo.objective_pct >= 0.9999 ? 4 : 2);
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-sm font-medium text-zinc-100">{slo.name}</h2>
        <span
          className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium ${
            slo.enabled
              ? "bg-emerald-900/50 text-emerald-300"
              : "bg-zinc-800 text-zinc-400"
          }`}
        >
          {slo.enabled ? "enabled" : "disabled"}
        </span>
      </div>
      {slo.description ? (
        <p className="mt-1 text-sm text-zinc-400">{slo.description}</p>
      ) : null}
      <div className="mt-3 grid gap-2 sm:grid-cols-3 text-xs text-zinc-400">
        <div>
          <span className="text-zinc-500">SLI: </span>
          <span className="font-mono">{slo.sli_kind}</span>
        </div>
        <div>
          <span className="text-zinc-500">Objective: </span>
          <span className="font-mono">{obj}%</span>
        </div>
        <div>
          <span className="text-zinc-500">Window: </span>
          <span className="font-mono">
            {days >= 1 ? `${days}d` : `${Math.round(slo.window_seconds / 60)}m`}
          </span>
        </div>
      </div>
      {Object.keys(slo.sli_filter ?? {}).length > 0 ? (
        <pre className="mt-3 rounded bg-zinc-950 p-2 text-[10px] font-mono text-zinc-300 whitespace-pre-wrap overflow-x-auto">
          {JSON.stringify(slo.sli_filter, null, 2)}
        </pre>
      ) : null}
    </div>
  );
}
