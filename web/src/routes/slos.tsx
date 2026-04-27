// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { listSLOs, type SLO, type SLOStatus } from "../api/client";

// What: /slos list view. One row per SLO with name + objective +
// SLI kind + window + filter summary + enabled badge + (v0.0.23)
// burn-rate state from the latest evaluator tick.
//
// State block (v0.0.23): the API now LEFT JOINs slo_state into the
// LIST response so we render last_status + last_burn_rate + last
// evaluated time inline per row. SLOs that have never been
// evaluated render with the "pending first evaluation" empty state.

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
          Burn rate &amp; status are reported from the evaluator's most
          recent tick (default 30s; runs alongside the controlplane).
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

// STATUS_CLASSES maps each SLO status to a Tailwind background +
// text colour. Mapping deliberately reuses the same colour vocabulary
// the canary anomaly card uses (emerald = healthy, amber = watch,
// orange/red = burning) so an operator who learns one status palette
// can read both. Unknown / no_data are neutral zinc — distinguishing
// them from the colour-loaded states is itself information.
const STATUS_CLASSES: Record<SLOStatus, string> = {
  unknown: "bg-zinc-800 text-zinc-400",
  no_data: "bg-zinc-800 text-zinc-300",
  ok: "bg-emerald-900/50 text-emerald-300",
  slow_burn: "bg-orange-900/50 text-orange-300",
  fast_burn: "bg-red-900/50 text-red-300",
};

const STATUS_LABEL: Record<SLOStatus, string> = {
  unknown: "unknown",
  no_data: "no data",
  ok: "ok",
  slow_burn: "slow burn",
  fast_burn: "fast burn",
};

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

      <SLOStateRow slo={slo} />

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

// SLOStateRow renders the LEFT-JOINed state. Three render branches:
//   - state === null → "pending first evaluation" empty state.
//   - state.last_status in {ok, no_data, unknown} → status pill +
//     evaluated-N-ago.
//   - state.last_status in {slow_burn, fast_burn} → red/orange pill +
//     burn-rate readout (formatted to 2dp) + evaluated-N-ago + (when
//     present) "alerted N ago".
function SLOStateRow({ slo }: { slo: SLO }) {
  if (!slo.state) {
    return (
      <p className="mt-3 text-xs text-zinc-500 italic">
        Pending first evaluation — the evaluator hasn't ticked for this
        SLO yet (default cadence 30s).
      </p>
    );
  }
  const s = slo.state;
  const cls = STATUS_CLASSES[s.last_status] ?? STATUS_CLASSES.unknown;
  const label = STATUS_LABEL[s.last_status] ?? s.last_status;
  const burning = s.last_status === "slow_burn" || s.last_status === "fast_burn";
  return (
    <div className="mt-3 flex flex-wrap items-baseline gap-3 text-xs">
      <span className={`inline-block rounded px-2 py-0.5 text-[10px] font-medium ${cls}`}>
        {label}
      </span>
      {burning ? (
        <span className="text-zinc-300">
          <span className="text-zinc-500">burn rate: </span>
          <span className="font-mono">{s.last_burn_rate.toFixed(2)}×</span>
        </span>
      ) : null}
      <span className="text-zinc-500 font-mono text-[10px]">
        evaluated {relativeAgo(s.last_evaluated_at)}
      </span>
      {s.last_alerted_at ? (
        <span className="text-zinc-500 font-mono text-[10px]">
          alerted {relativeAgo(s.last_alerted_at)}
        </span>
      ) : null}
    </div>
  );
}

// relativeAgo: same shape as canaries.tsx. Bounded at "just now"
// for sub-30s deltas; past-only (we don't render "in the future"
// because server clock can drift forward of the client by seconds).
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
