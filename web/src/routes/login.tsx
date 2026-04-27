// Copyright 2026 Shankar Reddy. BSL 1.1. See LICENSE.

import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { ApiError, login } from "../api/client";

// What: the login form. POST /v1/auth/login with tenant_id + email +
// password; the server sets a Secure HttpOnly session cookie and we
// navigate to /dashboard. There is no token storage on the client —
// the cookie is the source of truth, which keeps XSS exfiltration
// surface to zero.
//
// How: TanStack Query mutation wraps the API call. Field state is
// local React useState; the form is small enough that bringing in
// react-hook-form would be over-engineering. Submit disables until
// every field has content.
//
// Why we keep the password field type=password but don't auto-fill
// the tenant: most users belong to one tenant, but we don't know
// which one until the operator types it. A future onboarding flow
// can pre-populate (a magic-link in the welcome email).

export function LoginPage() {
  const navigate = useNavigate();
  const [tenantID, setTenantID] = useState("tnt-default");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const mut = useMutation({
    mutationFn: () =>
      login({ tenant_id: tenantID, email, password }),
    onSuccess: () => navigate({ to: "/dashboard" }),
  });

  const disabled =
    !tenantID.trim() ||
    !email.trim() ||
    password.length < 12 ||
    mut.isPending;

  return (
    <div className="mx-auto max-w-sm">
      <h1 className="text-xl font-semibold mb-1">Sign in</h1>
      <p className="text-sm text-zinc-400 mb-6">
        Enter your tenant ID, email, and password. Sessions are
        12-hour HttpOnly cookies; nothing is stored client-side.
      </p>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (!disabled) mut.mutate();
        }}
      >
        <Field
          label="Tenant ID"
          value={tenantID}
          onChange={setTenantID}
          mono
        />
        <Field label="Email" type="email" value={email} onChange={setEmail} />
        <Field
          label="Password"
          type="password"
          value={password}
          onChange={setPassword}
          help="Minimum 12 characters."
        />
        {mut.isError ? (
          <p className="text-sm text-red-400" role="alert">
            {renderError(mut.error)}
          </p>
        ) : null}
        <button
          type="submit"
          disabled={disabled}
          className="w-full rounded-md bg-sky-500 hover:bg-sky-400 disabled:bg-zinc-700 disabled:text-zinc-400 px-3 py-2 text-sm font-medium text-zinc-950 transition"
        >
          {mut.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}

function Field({
  label,
  value,
  onChange,
  type = "text",
  mono,
  help,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  type?: string;
  mono?: boolean;
  help?: string;
}) {
  return (
    <label className="block text-sm">
      <span className="text-zinc-300">{label}</span>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={`mt-1 block w-full rounded-md bg-zinc-900 border border-zinc-700 focus:border-sky-500 focus:outline-none px-3 py-2 ${mono ? "font-mono" : ""}`}
      />
      {help ? (
        <span className="text-xs text-zinc-500 mt-1 block">{help}</span>
      ) : null}
    </label>
  );
}

function renderError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 401) return "Wrong credentials.";
    return `Login failed (${err.status}). Try again.`;
  }
  return "Network error — is the control plane up?";
}
