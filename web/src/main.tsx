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

// What: the React entry point. TanStack Router for navigation,
// TanStack Query for server state, no Redux. The route tree below is
// declared programmatically (not file-based) so the v0.0.14 scaffold
// is small and reviewable; we'll migrate to file-based routes in
// v0.0.15 as the route count grows past four.
//
// How: createRootRoute + child routes for /login and /dashboard. The
// router is wrapped in a QueryClientProvider so any route can use
// useQuery / useMutation. The session-cookie auth flow lives in
// `src/api/client.ts`; routes call those helpers directly.
//
// Why no auth-router yet: Task 0.25's exit criterion is "login →
// dashboard renders live data". Anything fancier (route guards,
// lazy code-splitting per role) is v0.0.15+ work.

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

const routeTree = rootRoute.addChildren([
  indexRoute,
  loginRoute,
  dashboardRoute,
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
