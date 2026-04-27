// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

// What: thin typed wrapper over the NetSite control-plane REST API.
//
// How: every endpoint goes through `request<T>(path, init)` which sets
// `credentials: "include"` so the session cookie travels, normalises
// errors into ApiError, and parses JSON. Specific endpoint helpers
// (login, listTests, etc.) sit on top of it.
//
// Why a typed wrapper rather than raw fetch in components: components
// stay focused on rendering; tests can swap the wrapper for a fake;
// the surface that calls the API is small enough to keep current
// against api/openapi.yaml without auto-generated client churn.

export class ApiError extends Error {
  status: number;
  body: string;
  constructor(status: number, body: string) {
    super(`API ${status}: ${body || "(no body)"}`);
    this.status = status;
    this.body = body;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, body);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// ---- Health ---------------------------------------------------------

export interface Health {
  status: "ok";
  version: string;
  commit: string;
  built: string;
  backends: Record<string, string>;
}

export function getHealth(): Promise<Health> {
  return request<Health>("/v1/health");
}

// ---- Auth -----------------------------------------------------------

export interface LoginRequest {
  tenant_id: string;
  email: string;
  password: string;
}

export interface User {
  id: string;
  tenant_id: string;
  email: string;
  role: "viewer" | "operator" | "admin";
}

export function login(body: LoginRequest): Promise<User> {
  return request<User>("/v1/auth/login", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function logout(): Promise<void> {
  return request<void>("/v1/auth/logout", { method: "POST" });
}

export function whoami(): Promise<User> {
  return request<User>("/v1/auth/whoami");
}

// ---- Tests catalog --------------------------------------------------

export interface Test {
  id: string;
  tenant_id: string;
  kind: "dns" | "http" | "tls";
  target: string;
  interval_ms: number;
  timeout_ms: number;
  enabled: boolean;
  config: Record<string, unknown>;
}

export function listTests(): Promise<Test[]> {
  return request<Test[]>("/v1/tests");
}

// ---- SLOs -----------------------------------------------------------

export interface SLO {
  id: string;
  tenant_id: string;
  name: string;
  description: string;
  sli_kind: string;
  sli_filter: Record<string, unknown>;
  objective_pct: number;
  window_seconds: number;
  enabled: boolean;
}

export function listSLOs(): Promise<SLO[]> {
  return request<SLO[]>("/v1/slos");
}

// ---- Workspaces -----------------------------------------------------

export interface Workspace {
  id: string;
  name: string;
  description: string;
  views: Array<{ name: string; url: string; note?: string }>;
  share_slug?: string;
  share_expires_at?: string | null;
  created_at: string;
  updated_at: string;
}

export function listWorkspaces(): Promise<Workspace[]> {
  return request<Workspace[]>("/v1/workspaces");
}

// ---- Annotations ---------------------------------------------------

export interface Annotation {
  id: string;
  tenant_id: string;
  scope: "canary" | "pop" | "test";
  scope_id: string;
  at: string;
  body_md: string;
  author_id: string;
  created_at: string;
}

export interface AnnotationListFilter {
  scope?: Annotation["scope"];
  scope_id?: string;
  from?: string;
  to?: string;
  limit?: number;
}

export function listAnnotations(
  f: AnnotationListFilter = {},
): Promise<Annotation[]> {
  const params = new URLSearchParams();
  if (f.scope) params.set("scope", f.scope);
  if (f.scope_id) params.set("scope_id", f.scope_id);
  if (f.from) params.set("from", f.from);
  if (f.to) params.set("to", f.to);
  if (f.limit) params.set("limit", String(f.limit));
  const qs = params.toString();
  return request<Annotation[]>(`/v1/annotations${qs ? `?${qs}` : ""}`);
}
