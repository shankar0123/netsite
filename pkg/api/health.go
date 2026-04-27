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

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/shankar0123/netsite/pkg/version"
)

// healthResponse is the JSON body shape returned by /v1/health.
//
// Why publish version inline: makes "what version is this server" a
// scriptable check (`curl /v1/health | jq .version`) without needing
// auth. Operators and load balancers both rely on this.
type healthResponse struct {
	Status   string            `json:"status"`
	Version  string            `json:"version"`
	Commit   string            `json:"commit"`
	Built    string            `json:"built"`
	Backends map[string]string `json:"backends"`
}

// healthHandler returns a handler that reports the server's liveness
// and the reachability of each backend (Postgres for now; ClickHouse
// and NATS in later tasks).
//
// The handler probes each backend with a 1-second timeout. A failing
// probe sets the corresponding map entry to "down" but does not flip
// the top-level Status — load balancers that scrape /v1/health expect
// 200 as long as the process is up. Backend-specific 5xx behavior is
// the responsibility of the dependent endpoints, not the health
// endpoint.
func healthHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		resp := healthResponse{
			Status:   "ok",
			Version:  version.Version,
			Commit:   version.Commit,
			Built:    version.BuildDate,
			Backends: map[string]string{},
		}

		if err := cfg.Pool.Ping(ctx); err != nil {
			resp.Backends["postgres"] = "down: " + err.Error()
		} else {
			resp.Backends["postgres"] = "up"
		}

		w.Header().Set("Content-Type", "application/json")
		// Always 200 — see comment block above for rationale.
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})
}
