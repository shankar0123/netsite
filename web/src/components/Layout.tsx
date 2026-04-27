// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

// What: the top-level chrome — a dark-mode shell with a thin top bar
// (brand + global nav) and a body that hosts the active route.
//
// How: a plain flex column. Routes render inside <Outlet/> from the
// root route; this component just wraps them with the persistent
// chrome. The nav is a flat list of <Link> elements so TanStack
// Router's active-route highlighting works without any extra state.
//
// Why a single-file shell: with seven nav items the bar is still a
// readable single line on desktop and an honest horizontal scroll on
// mobile. When the v1 cut adds /bgp, /flow, /pcap, /correlation we
// move to a sidebar — not before. Pre-building a sidebar for routes
// that don't exist is dead code, and dead code is the most expensive
// kind of code in a codebase that's heading into acquisition
// diligence.

interface NavItem {
  to: string;
  label: string;
  exact?: boolean;
}

const NAV: NavItem[] = [
  { to: "/dashboard", label: "Dashboard" },
  { to: "/canaries", label: "Canaries" },
  { to: "/slos", label: "SLOs" },
  { to: "/workspaces", label: "Workspaces" },
  { to: "/annotations", label: "Annotations" },
  { to: "/netql", label: "netql" },
  { to: "/login", label: "Login", exact: true },
];

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
          <nav className="flex gap-4 text-sm text-zinc-400 overflow-x-auto">
            {NAV.map((n) => (
              <Link
                key={n.to}
                to={n.to}
                className="hover:text-zinc-100 whitespace-nowrap"
                activeOptions={n.exact ? { exact: true } : { exact: false }}
                activeProps={{ className: "text-zinc-100" }}
              >
                {n.label}
              </Link>
            ))}
          </nav>
        </div>
      </header>
      <main className="flex-1 mx-auto w-full max-w-6xl px-4 py-8">
        {children}
      </main>
      <footer className="border-t border-zinc-800 px-4 py-3 text-xs text-zinc-500">
        <div className="mx-auto max-w-6xl flex items-center justify-between">
          <span>NetSite — self-hosted network observability</span>
          <span className="font-mono">v0.0.22</span>
        </div>
      </footer>
    </div>
  );
}
