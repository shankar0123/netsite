// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

// netql REST client. Two endpoints: translate (pure compile, the
// "show me the SQL" reveal) and execute (translate + run against
// ClickHouse, return columnar rows). Lives in its own file because
// the chart-rendering route is the only consumer; keeping it
// separate from client.ts keeps that file free of result-shape
// types we'd otherwise have to invent.

export interface NetQLTranslateResponse {
  backend: "clickhouse" | "prometheus";
  metric: string;
  sql?: string;
  promql?: string;
  args?: unknown[];
}

export interface NetQLExecuteResponse extends NetQLTranslateResponse {
  columns: string[];
  rows: unknown[][];
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status}: ${text}`);
  }
  return (await res.json()) as T;
}

export function translate(query: string): Promise<NetQLTranslateResponse> {
  return post<NetQLTranslateResponse>("/v1/netql/translate", { query });
}

export function execute(query: string): Promise<NetQLExecuteResponse> {
  return post<NetQLExecuteResponse>("/v1/netql/execute", { query });
}
