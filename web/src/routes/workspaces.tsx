// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useQuery } from "@tanstack/react-query";
import { listWorkspaces, type Workspace } from "../api/client";

// What: /workspaces list view. Saved-view bundles per Task 0.23.
// Read-only in v0.0.18; Create + Update + Share UI lands in
// v0.0.19 alongside the workspace-URL-deep-link state restore
// (which closes the "Workspace URL persists" exit-gate criterion).

export function WorkspacesPage() {
  const q = useQuery({ queryKey: ["workspaces"], queryFn: listWorkspaces });
  if (q.isPending) return <p className="text-sm text-zinc-500">Loading…</p>;
  if (q.isError) return <p className="text-sm text-red-400">Failed to load workspaces.</p>;
  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Workspaces</h1>
        <p className="text-sm text-zinc-400">
          Saved-view bundles in this tenant.
        </p>
      </header>
      {q.data.length === 0 ? (
        <p className="text-sm text-zinc-500">
          No workspaces yet. POST one to{" "}
          <code className="font-mono">/v1/workspaces</code>.
        </p>
      ) : (
        <div className="space-y-3">
          {q.data.map((w) => (
            <WorkspaceRow key={w.id} ws={w} />
          ))}
        </div>
      )}
    </div>
  );
}

function WorkspaceRow({ ws }: { ws: Workspace }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-sm font-medium text-zinc-100">{ws.name}</h2>
        {ws.share_slug ? (
          <span className="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-sky-900/50 text-sky-300 font-mono">
            shared · {ws.share_slug.slice(0, 8)}…
          </span>
        ) : null}
      </div>
      {ws.description ? (
        <p className="mt-1 text-sm text-zinc-400">{ws.description}</p>
      ) : null}
      <p className="mt-2 text-xs text-zinc-500">
        {ws.views.length} pinned view{ws.views.length === 1 ? "" : "s"} · created{" "}
        {new Date(ws.created_at).toLocaleString()}
      </p>
      {ws.views.length > 0 ? (
        <ul className="mt-3 space-y-1 text-sm">
          {ws.views.slice(0, 5).map((v, i) => (
            <li key={i} className="flex items-center justify-between gap-2">
              <span className="truncate text-zinc-300">{v.name}</span>
              <a
                href={v.url}
                className="font-mono text-[10px] text-sky-400 hover:text-sky-300 truncate"
                target="_blank"
                rel="noreferrer"
              >
                {v.url}
              </a>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
