// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useState } from "react";
import { execute, translate, type NetQLExecuteResponse } from "../api/netql";
import { BarChart } from "../components/Chart";

// What: the /netql route. Textarea-based editor (Monaco arrives
// in v0.0.18+ alongside autocomplete wiring), "Translate" button
// reveals SQL/PromQL, "Run" button executes and renders a chart
// + a results table.
//
// How: two TanStack Query mutations would be over-engineering for
// a single-button-click flow; we use plain async / useState and
// catch errors into a state field. The textarea is intentionally
// pre-populated with the canonical seven-word example from the
// algorithm doc so first-time visitors have something to click
// "Run" on.
//
// Why a textarea rather than Monaco for v0.0.17: Monaco is ~3 MB
// gzipped, dwarfs the rest of the bundle, and forces us to
// resolve a worker-loading story before the page can be served.
// Save it for v0.0.18 when we wire autocomplete to /v1/netql/...
// (the autocomplete API call is the thing that actually needs
// Monaco's editor.registerCompletionItemProvider hook).

const EXAMPLE_QUERIES = [
  "latency_p95 by pop where target = 'api.example.com' over 24h",
  "success_rate by pop over 1h",
  "count by error_kind where kind = 'http' over 7d",
  "request_rate by route over 5m",
];

export function NetQLPage() {
  const [query, setQuery] = useState(EXAMPLE_QUERIES[0]);
  const [result, setResult] = useState<NetQLExecuteResponse | null>(null);
  const [translation, setTranslation] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"translate" | "run" | null>(null);

  async function onTranslate() {
    setBusy("translate");
    setError(null);
    try {
      const tr = await translate(query);
      setTranslation(tr.backend === "clickhouse" ? tr.sql ?? "" : tr.promql ?? "");
      setResult(null);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  }

  async function onRun() {
    setBusy("run");
    setError(null);
    try {
      const out = await execute(query);
      setResult(out);
      setTranslation(out.backend === "clickhouse" ? out.sql ?? "" : out.promql ?? "");
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">netql</h1>
        <p className="text-sm text-zinc-400">
          Write a query, see the SQL or PromQL it compiles to, run
          it, see the result.
        </p>
      </header>

      <section className="space-y-3">
        <label className="block text-sm">
          <span className="text-zinc-300">Query</span>
          <textarea
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            rows={3}
            className="mt-1 block w-full rounded-md bg-zinc-900 border border-zinc-700 focus:border-sky-500 focus:outline-none px-3 py-2 font-mono text-sm"
            spellCheck={false}
          />
        </label>
        <div className="flex gap-2 flex-wrap">
          <button
            type="button"
            onClick={onTranslate}
            disabled={busy !== null || !query.trim()}
            className="rounded-md bg-zinc-800 hover:bg-zinc-700 disabled:bg-zinc-900 disabled:text-zinc-600 px-3 py-1.5 text-sm font-medium text-zinc-100 transition border border-zinc-700"
          >
            {busy === "translate" ? "Translating…" : "Show SQL"}
          </button>
          <button
            type="button"
            onClick={onRun}
            disabled={busy !== null || !query.trim()}
            className="rounded-md bg-sky-500 hover:bg-sky-400 disabled:bg-zinc-700 disabled:text-zinc-400 px-3 py-1.5 text-sm font-medium text-zinc-950 transition"
          >
            {busy === "run" ? "Running…" : "Run"}
          </button>
          <div className="flex-1" />
          <select
            value=""
            onChange={(e) => {
              if (e.target.value) setQuery(e.target.value);
            }}
            className="rounded-md bg-zinc-900 border border-zinc-700 px-2 py-1.5 text-xs"
          >
            <option value="">Examples…</option>
            {EXAMPLE_QUERIES.map((q) => (
              <option key={q} value={q}>
                {q}
              </option>
            ))}
          </select>
        </div>
        {error ? (
          <pre
            className="rounded-md bg-red-950/50 text-red-200 p-3 text-xs font-mono whitespace-pre-wrap"
            role="alert"
          >
            {error}
          </pre>
        ) : null}
      </section>

      {translation ? (
        <section>
          <h2 className="text-sm font-medium text-zinc-200 mb-2">
            Compiled {result?.backend === "prometheus" ? "PromQL" : "SQL"}
          </h2>
          <pre className="rounded-md bg-zinc-950 border border-zinc-800 p-3 text-xs font-mono whitespace-pre-wrap text-zinc-200 overflow-x-auto">
            {translation}
          </pre>
        </section>
      ) : null}

      {result && result.rows.length > 0 ? (
        <section className="space-y-4">
          <h2 className="text-sm font-medium text-zinc-200">Result</h2>
          <div className="rounded-md bg-zinc-900/50 border border-zinc-800 p-4">
            <BarChart columns={result.columns} rows={result.rows} />
          </div>
          <div className="rounded-md bg-zinc-900/50 border border-zinc-800 overflow-x-auto">
            <table className="text-sm w-full">
              <thead>
                <tr className="text-left text-zinc-400 border-b border-zinc-800">
                  {result.columns.map((c) => (
                    <th key={c} className="px-3 py-2 font-mono text-xs">
                      {c}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {result.rows.map((row, i) => (
                  <tr key={i} className="border-b border-zinc-800/50">
                    {row.map((cell, j) => (
                      <td key={j} className="px-3 py-2 font-mono text-xs">
                        {String(cell)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {result && result.rows.length === 0 ? (
        <p className="text-sm text-zinc-500 italic">
          Query ran successfully — zero rows returned. Check the
          time range or filters.
        </p>
      ) : null}
    </div>
  );
}
