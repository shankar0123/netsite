// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import React from "react";
import ReactDOM from "react-dom/client";
import {
  RouterProvider,
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
} from "@tanstack/react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import "./styles.css";
import { Layout } from "./components/Layout";
import { LoginPage } from "./routes/login";
import { DashboardPage } from "./routes/dashboard";
import { NetQLPage } from "./routes/netql";
import { CanariesPage, CanaryDetailPage } from "./routes/canaries";
import { SLOsPage } from "./routes/slos";
import { WorkspacesPage } from "./routes/workspaces";
import { AnnotationsPage } from "./routes/annotations";

// What: the React entry point. TanStack Router for navigation,
// TanStack Query for server state, no Redux.
//
// How: createRootRoute + child routes for the nine v0.0.18 surfaces
// (/, /login, /dashboard, /netql, /canaries, /canaries/$id, /slos,
// /workspaces, /annotations). The router is wrapped in a
// QueryClientProvider so any route can use useQuery / useMutation.
// The session-cookie auth flow lives in `src/api/client.ts`; routes
// call those helpers directly.
//
// Why programmatic-not-file-based routes: with nine routes the
// programmatic tree is still under ~50 lines and stays grep-able in
// a single file. The migration to file-based routes is the v0.0.21
// pre-Phase-1 housekeeping pass — at that point /bgp, /flow, /pcap
// will have multiplied the count past the readability threshold.
//
// Why no auth-router yet: Phase 0 exit-gate criterion is "login →
// dashboard renders live data". Route guards / role-based lazy
// chunks are Phase 5 (RBAC) work.

const rootRoute = createRootRoute({
  component: () => (
    <Layout>
      <Outlet />
    </Layout>
  ),
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: DashboardPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
});

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/dashboard",
  component: DashboardPage,
});

const netqlRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/netql",
  component: NetQLPage,
});

const canariesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/canaries",
  component: CanariesPage,
});

const canaryDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/canaries/$id",
  component: CanaryDetailPage,
});

const slosRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/slos",
  component: SLOsPage,
});

const workspacesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workspaces",
  component: WorkspacesPage,
});

const annotationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/annotations",
  component: AnnotationsPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  dashboardRoute,
  netqlRoute,
  canariesRoute,
  canaryDetailRoute,
  slosRoute,
  workspacesRoute,
  annotationsRoute,
]);

const router = createRouter({ routeTree });

// Single QueryClient per app instance. Default staleTime is 30s so a
// quick navigation back to a list page doesn't re-fetch needlessly;
// individual queries override when freshness matters.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
});

// Type augmentation for TanStack Router so useNavigate / Link have
// strong typing.
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root element in index.html");

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
);
