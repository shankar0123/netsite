// Copyright 2026 Shankar Reddy. All Rights Reserved.
//
// Licensed under the Business Source License 1.1 (the "License").
// Licensed Work: NetSite. Change Date: 2125-01-01.
// Change License: Apache License, Version 2.0.

import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import fs from "node:fs";
import path from "node:path";

// What: Vite config for the NetSite web shell.
//
// How:
//   - `vite` (default mode) starts the dev server on http://localhost:5173
//     for the lowest-friction first-touch experience.
//   - `vite --mode secure` (aka `pnpm dev-secure`) reads the mkcert pair
//     under deploy/dev-certs/ and binds HTTPS instead. This is the
//     recommended local-dev path because it matches the production
//     posture (CLAUDE.md A11) and lets us use Secure cookies + future
//     SameSite=None cross-origin to the API on :8443.
//
// Why two modes: an operator running `pnpm dev` for the first time
// shouldn't have to install mkcert before they see anything; the
// plain-HTTP path is acceptable for a 2-minute first look as long as
// the documentation steers them to `pnpm dev-secure` afterwards.
//
// Anything that talks to the control plane goes through src/api/client.ts
// — never raw fetch — so the dev-server proxy below routes /v1/* to the
// running ns-controlplane and keeps cookies same-origin during dev.

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "NETSITE_");

  // Default upstream is the plaintext compose flow (NETSITE_CONTROLPLANE_
  // ALLOW_PLAINTEXT=true behind Caddy). Secure mode points at AutoTLS
  // or the mkcert TLS-listen flow on :8443.
  const apiTarget =
    env.NETSITE_API_TARGET ||
    (mode === "secure" ? "https://localhost:8443" : "http://localhost:8080");

  // mkcert cert pair under deploy/dev-certs/. Only loaded in secure mode;
  // missing files are a hard error so the operator knows to run
  // `make dev-tls`.
  const httpsConfig = (() => {
    if (mode !== "secure") return undefined;
    const certPath = path.resolve("../deploy/dev-certs/localhost.pem");
    const keyPath = path.resolve("../deploy/dev-certs/localhost-key.pem");
    if (!fs.existsSync(certPath) || !fs.existsSync(keyPath)) {
      throw new Error(
        `secure mode requires mkcert certs at ${certPath} and ${keyPath}; run \`make dev-tls\` from the repo root first.`,
      );
    }
    return {
      cert: fs.readFileSync(certPath),
      key: fs.readFileSync(keyPath),
    };
  })();

  return {
    plugins: [react(), tailwindcss()],
    server: {
      port: 5173,
      strictPort: true,
      https: httpsConfig,
      proxy: {
        // /v1/* → ns-controlplane. The proxy preserves cookies so the
        // session flow stays same-origin from the browser's POV.
        "/v1": {
          target: apiTarget,
          changeOrigin: true,
          // Accept the mkcert / AutoTLS self-signed cert in dev.
          // Production has a real cert; this knob only matters here.
          secure: false,
        },
      },
    },
    build: {
      // ns-controlplane embeds web/dist via go:embed. Keep the output
      // path stable — the embed.go pattern uses it verbatim.
      outDir: "dist",
      // Inline assets ≤ 4 KiB; everything else gets a content-hashed
      // filename so the SPA fallback in ns-controlplane can serve
      // them with long cache lifetimes.
      assetsInlineLimit: 4096,
    },
  };
});
