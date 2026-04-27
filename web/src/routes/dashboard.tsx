// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  ApiError,
  getHealth,
  listTests,
  listSLOs,
  whoami,
} from "../api/client";

// What: the post-login landing page. Shows three cards:
//   - whoami (resolved from /v1/auth/whoami so anonymous visitors
//     get bounced to /login).
//   - canary tests catalog snapshot (count + a few rows).
//   - SLOs catalog snapshot (count + a few rows).
//
// How: three TanStack Query hooks; loading/error/empty render
// branches per card. If whoami returns 401 we route to /login.
//
// Why three cards rather than a full split-pane: this is the
// scaffold. Once Tasks 0.22 (netql) and 0.23 (workspaces) are wired
// into the UI, the dashboard becomes a workspace renderer; the
// cards here are the placeholder. v0.0.18 wires the "View all"
// links to the new /canaries, /slos, /workspaces, /annotations
// surfaces — the dashboard is still a snapshot, but each card now
// gestures at where the deeper view lives.

export function DashboardPage() {
  const navigate = useNavigate();
  const meQ = useQuery({
    queryKey: ["whoami"],
    queryFn: whoami,
    retry: false,
  });
  const healthQ = useQuery({ queryKey: ["health"], queryFn: getHealth });
  const testsQ = useQuery({ queryKey: ["tests"], queryFn: listTests });
  const slosQ = useQuery({ queryKey: ["slos"], queryFn: listSLOs });

  if (meQ.isError) {
    const err = meQ.error;
    if (err instanceof ApiError && err.status === 401) {
      navigate({ to: "/login" });
      return null;
    }
  }

  return (
    <div className="space-y-8">
      <header>
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <p className="text-sm text-zinc-400">
          {meQ.data
            ? `Signed in as ${meQ.data.email} (${meQ.data.role}) — tenant ${meQ.data.tenant_id}`
            : "Loading session…"}
        </p>
      </header>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Card title="Backend health">
          {healthQ.isPending ? (
            <Loading />
          ) : healthQ.isError ? (
            <ErrorMessage err={healthQ.error} />
          ) : (
            <ul className="space-y-1 text-sm">
              <li>
                <span className="text-zinc-400">version: </span>
                <span className="font-mono">{healthQ.data.version}</span>
              </li>
              <li>
                <span className="text-zinc-400">commit: </span>
                <span className="font-mono">{healthQ.data.commit}</span>
              </li>
              {Object.entries(healthQ.data.backends).map(([k, v]) => (
                <li key={k}>
                  <span className="text-zinc-400">{k}: </span>
                  <Pill ok={v === "up"}>{v}</Pill>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card
          title={`Canary tests (${testsQ.data?.length ?? "—"})`}
          link={{ to: "/canaries", text: "View all" }}
        >
          {testsQ.isPending ? (
            <Loading />
          ) : testsQ.isError ? (
            <ErrorMessage err={testsQ.error} />
          ) : testsQ.data.length === 0 ? (
            <Empty msg="No tests yet. Configure one via /v1/tests." />
          ) : (
            <ul className="space-y-1 text-sm">
              {testsQ.data.slice(0, 4).map((t) => (
                <li
                  key={t.id}
                  className="flex items-center justify-between gap-2"
                >
                  <span className="font-mono text-xs text-zinc-300 truncate">
                    {t.id}
                  </span>
                  <span className="text-xs text-zinc-500">
                    {t.kind} · {Math.round(t.interval_ms / 1000)}s
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card
          title={`SLOs (${slosQ.data?.length ?? "—"})`}
          link={{ to: "/slos", text: "View all" }}
        >
          {slosQ.isPending ? (
            <Loading />
          ) : slosQ.isError ? (
            <ErrorMessage err={slosQ.error} />
          ) : slosQ.data.length === 0 ? (
            <Empty msg="No SLOs yet. POST one to /v1/slos." />
          ) : (
            <ul className="space-y-1 text-sm">
              {slosQ.data.slice(0, 4).map((s) => (
                <li key={s.id} className="flex items-center justify-between gap-2">
                  <span className="truncate text-zinc-200">{s.name}</span>
                  <span className="text-xs text-zinc-500 font-mono">
                    {Math.round(s.objective_pct * 1000) / 10}%
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}

function Card({
  title,
  link,
  children,
}: {
  title: string;
  link?: { to: string; text: string };
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
      <header className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-medium text-zinc-200">{title}</h2>
        {link ? (
          <Link
            to={link.to}
            className="text-xs text-sky-400 hover:text-sky-300"
          >
            {link.text}
          </Link>
        ) : null}
      </header>
      {children}
    </section>
  );
}

function Loading() {
  return <p className="text-xs text-zinc-500">Loading…</p>;
}

function Empty({ msg }: { msg: string }) {
  return <p className="text-xs text-zinc-500">{msg}</p>;
}

function ErrorMessage({ err }: { err: unknown }) {
  const msg =
    err instanceof ApiError
      ? `${err.status}: ${err.body || "(no body)"}`
      : "Network error";
  return (
    <p className="text-xs text-red-400" role="alert">
      {msg}
    </p>
  );
}

function Pill({
  children,
  ok,
}: {
  children: React.ReactNode;
  ok: boolean;
}) {
  return (
    <span
      className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium ${
        ok ? "bg-emerald-900/50 text-emerald-300" : "bg-red-900/50 text-red-300"
      }`}
    >
      {children}
    </span>
  );
}
