// Copyright 2026 Shankar Reddy. All Rights Reserved.
//
// Licensed under the Business Source License 1.1 (the "License").
// You may not use this file except in compliance with the License.
// A copy of the License is bundled with this distribution at ./LICENSE
// in the repository root, or available at https://mariadb.com/bsl11/.
//
// Licensed Work:  NetSite
// Change Date:    2125-01-01
// Change License: Apache License, Version 2.0
//
// On the Change Date, the rights granted in this License terminate and
// you are granted rights under the Change License instead.

package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// What: embed the Vite-built React shell and serve it from the
// control plane. Static assets at /assets/* (content-hashed by
// Vite, safe for long cache lifetimes); the SPA fallback serves
// index.html for every other path so client-side routing works on
// page reload.
//
// How: //go:embed pulls web/dist/ into the binary at compile time.
// At runtime we strip the `dist/` prefix and serve the resulting
// fs.FS via http.FileServer. The fallback handler intercepts 404s
// and re-serves index.html.
//
// Why embed rather than ship the SPA separately: single-binary
// deploys are an architecture goal (one ns-controlplane binary,
// nothing else on disk). Air-gap deployments specifically benefit:
// the operator transfers one signed binary, not a binary plus a
// dist tarball.
//
// `all:web/dist` includes dotfiles (Vite emits `.vite/manifest.json`
// for chunked output bookkeeping). The `web/dist/_empty` placeholder
// keeps the embed directive valid even when the frontend hasn't
// been built yet — `pnpm build` in web/ replaces the placeholder
// with the real bundle.

//go:embed all:web/dist
var webDist embed.FS

// webHandler returns an http.Handler that serves the embedded SPA
// or, if the embedded tree is empty (developer running the binary
// without first building the frontend), an HTML stub explaining
// what to do.
func webHandler() http.Handler {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		// Compile-time guarantee that web/dist exists; this can only
		// happen if the embed directive is removed and not replaced.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web bundle not available: "+err.Error(), http.StatusInternalServerError)
		})
	}

	// Detect the "empty placeholder" case where _empty is the only
	// file. If so, render a one-screen explanation rather than a
	// blank page.
	if !hasIndex(sub) {
		return http.HandlerFunc(missingBundleHandler)
	}

	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static assets (Vite emits content-hashed filenames so
		// these are safe to long-cache). Everything else falls
		// through to index.html so the SPA router can handle it.
		if strings.HasPrefix(r.URL.Path, "/assets/") ||
			strings.HasPrefix(r.URL.Path, "/favicon") {
			fileSrv.ServeHTTP(w, r)
			return
		}
		// SPA fallback: read index.html and serve it as the
		// response body for any unmatched path.
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "missing index.html", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// No-cache on index.html so a deploy that ships a new
		// asset hash is picked up by the next page load.
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// hasIndex reports whether the embedded fs has an index.html. The
// embed always succeeds (web/dist/_empty is committed); this check
// distinguishes "real bundle" from "placeholder only".
func hasIndex(sub fs.FS) bool {
	_, err := fs.Stat(sub, "index.html")
	return err == nil
}

// missingBundleHandler explains how to build the frontend when the
// binary was compiled without one. Friendly developer-facing copy;
// production deploys ship a built binary so this path never runs.
func missingBundleHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>NetSite — frontend not built</title></head>
<body style="font-family: system-ui; background: #0a0a0c; color: #f4f4f5; padding: 4rem; max-width: 32rem; margin: 0 auto;">
<h1>Frontend not built</h1>
<p>This <code>ns-controlplane</code> binary was compiled without the React shell.</p>
<p>To build it:</p>
<pre style="background: #18181b; padding: 1rem; border-radius: 0.5rem;">cd web
pnpm install
pnpm build
cd ..
go build ./cmd/ns-controlplane</pre>
<p>The API surface at <code>/v1/*</code> still works without the frontend.</p>
</body></html>
`))
}
