# NetSite web shell

> The browser-facing UI for the NetSite control plane. Vite 6 +
> React 19 + TanStack Router + TanStack Query + Tailwind v4.
> Built into `web/dist/` and embedded into `ns-controlplane` via
> `//go:embed` so the whole product ships as one Go binary.
>
> If you want the operator-level intro (architecture, what
> the dashboard means, how to log in), read
> [`../docs/getting-started.md`](../docs/getting-started.md).
> This README is for frontend developers.

## Prereqs

- **Node ≥ 20.**
- **pnpm ≥ 9.** Install via `corepack enable` (Node bundles
  Corepack, which then installs pnpm) or `brew install pnpm`.

That's it. Vite handles everything else.

## Quickstart

```sh
cd web
pnpm install
pnpm dev
```

Vite binds `http://localhost:5173`. The proxy config at
`vite.config.ts` forwards `/v1/*` to `http://localhost:8080`
(default plaintext compose flow). Run the controlplane there
via `make run-controlplane` from the repo root.

For HTTPS (matching the production TLS posture, see
[`../docs/security.md`](../docs/security.md)):

```sh
# One-time, from the repo root.
make dev-tls

# Then, from web/.
pnpm dev-secure
```

`pnpm dev-secure` reads the mkcert pair under
`../deploy/dev-certs/` and binds HTTPS. The proxy now points at
`https://localhost:8443` so the controlplane needs to run in
TLS-listen mode (`make run-controlplane-tls` from the repo root).

## Build for embed

```sh
# From the repo root.
make build-all
```

This chains `pnpm install + pnpm build` (under `web/`), copies
`web/dist/` into `cmd/ns-controlplane/web/dist/` (where the
`//go:embed` directive expects it), and builds the Go binaries
with the SPA embedded. Run the resulting `./ns-controlplane` —
the dashboard renders at `/`, the API surface lives at `/v1/*`,
the SPA fallback handles client-side routing on page reload.

## Where things live

```
web/
├── package.json          dependencies + scripts (pnpm dev / build / lint / test)
├── tsconfig.json         strict TypeScript config
├── vite.config.ts        two-mode dev server (http / https), /v1/* proxy
├── index.html            empty HTML shell with #root and the styles import
└── src/
    ├── main.tsx          TanStack Router + QueryClientProvider entry
    ├── styles.css        Tailwind v4 import + theme tokens
    ├── api/
    │   └── client.ts     typed wrapper over the full /v1/* surface
    ├── components/
    │   └── Layout.tsx    top bar + main slot + footer chrome
    └── routes/
        ├── login.tsx     POST /v1/auth/login → Secure cookie → /dashboard
        └── dashboard.tsx whoami / health / tests / SLOs cards
```

Routes are declared programmatically in `main.tsx` for now (small
v0 surface). When the route count grows past four we'll migrate
to TanStack Router's file-based routing — but not before; the
programmatic form is easier to read at this scale.

## API client conventions

`src/api/client.ts` is the only place that calls `fetch`.
Components never call `fetch` directly. The reason:

- **Cookies travel uniformly.** Every request uses
  `credentials: "include"` so the session cookie is sent without
  per-call boilerplate.
- **Errors normalise.** Non-2xx responses throw `ApiError(status,
  body)`; components branch on `err instanceof ApiError &&
  err.status === 401` to handle "anonymous" cleanly.
- **Types stay close to the OpenAPI surface.** When the API
  changes, you change one file, not the call sites.

Add a new endpoint by writing one function in `client.ts`. Tests
that need to mock the API mock the function, not `fetch`.

## State management

- **Server state** → TanStack Query. Use `useQuery` for reads,
  `useMutation` for writes. Default `staleTime` is 30 seconds
  (`main.tsx`); per-query overrides when freshness matters.
- **App state** (auth status, theme toggle when it lands) →
  React Context. Don't reach for Redux.
- **Form state** → React `useState` for small forms (login).
  Bring in `react-hook-form` if a form has more than 4 fields
  and dynamic validation; we don't have one yet.

## Styling

Tailwind v4 with a single `@import "tailwindcss"` in `styles.css`.
Dark mode is on by default — `<html class="dark">` in `index.html`.
Theme tokens (`--color-bg`, `--color-fg`, `--color-accent`,
`--font-sans`, `--font-mono`) live in `:root` for places where a
Tailwind utility doesn't exist yet.

The font stack is system-only by default (Inter / JetBrains Mono
via `system-ui` fallback). Bundling webfonts is a Phase 1 add
once the air-gap deployment story needs offline fonts.

## Testing

Vitest is wired but no tests yet (the v0.0.14 scaffold is small
enough that the type-checker + manual walk-through is adequate
coverage). Tests land alongside route expansion in v0.0.15+:

- **Unit / component tests** for individual route components,
  mocked API client.
- **Playwright smoke** for the Phase 0 exit-gate flow: login →
  canary detail → workspace save. Lives under
  `web/playwright/` (not yet committed).

## What's coming

v0.0.15 plan:

- `/canaries` — list view + per-canary detail timeline +
  annotation markers.
- `/slos` — list view + burn-rate chart per SLO.
- `/netql` — Monaco editor with autocomplete bound to
  `/v1/netql` (when that REST endpoint lands), "show me the SQL"
  reveal panel.
- `/workspaces` — list + edit + share-link UI.
- `/annotations` — list with scope/timeline filters.

Phase 1+:

- MapLibre GL JS for the BGP / route-leak visualisations.
- visx + Recharts for time-series charts (drop-in replacement of
  the placeholder pills in v0.0.14).
- shadcn/ui for richer form components (combobox, date picker)
  as the route surface grows.
