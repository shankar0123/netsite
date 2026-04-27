// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

// What: the top-level chrome — a dark-mode shell with a thin top bar
// (brand + global nav) and a body that hosts the active route.
//
// How: a plain flex column. Routes render inside <Outlet/> from the
// root route; this component just wraps them with the persistent
// chrome.
//
// Why a single-file shell: keeps v0.0.14 scaffolding tight. When the
// nav grows past four items we'll split the bar into its own file
// and add a sidebar — but we don't pre-build that.
export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen flex flex-col">
      <header className="border-b border-zinc-800 bg-zinc-900/60 backdrop-blur">
        <div className="mx-auto max-w-6xl px-4 py-3 flex items-center gap-6">
          <Link
            to="/"
            className="font-mono text-sm tracking-wider text-sky-400 hover:text-sky-300"
          >
            NetSite
          </Link>
          <nav className="flex gap-4 text-sm text-zinc-400">
            <Link
              to="/dashboard"
              className="hover:text-zinc-100"
              activeOptions={{ exact: false }}
              activeProps={{ className: "text-zinc-100" }}
            >
              Dashboard
            </Link>
            <Link
              to="/netql"
              className="hover:text-zinc-100"
              activeProps={{ className: "text-zinc-100" }}
            >
              netql
            </Link>
            <Link
              to="/login"
              className="hover:text-zinc-100"
              activeProps={{ className: "text-zinc-100" }}
            >
              Login
            </Link>
          </nav>
        </div>
      </header>
      <main className="flex-1 mx-auto w-full max-w-6xl px-4 py-8">
        {children}
      </main>
      <footer className="border-t border-zinc-800 px-4 py-3 text-xs text-zinc-500">
        <div className="mx-auto max-w-6xl flex items-center justify-between">
          <span>NetSite — self-hosted network observability</span>
          <span className="font-mono">v0.0.14</span>
        </div>
      </footer>
    </div>
  );
}
