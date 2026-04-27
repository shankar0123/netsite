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

// Package clickhouse is the ClickHouse data-access layer for the NetSite
// control plane and its workers.
//
// What: this package owns the ClickHouse connection (using the official
// `clickhouse-go/v2` native-protocol driver), the schema-as-code
// applier, and (in later phases) high-cardinality time-series writers
// for canary results, BGP updates, flow records, and PCAP-derived
// fingerprints.
//
// How: Open() parses a DSN of the form
//
//	clickhouse://user:pass@host:9000/dbname?dial_timeout=10s
//
// and returns a configured driver.Conn. Apply() runs SQL files from an
// embed.FS in lexicographic order, tracking applied names in a
// `_ch_schema_applied` ReplacingMergeTree table so re-runs are no-ops.
//
// Why ClickHouse for time-series and Postgres for relational config
// (not "ClickHouse for everything"): ClickHouse's columnar storage and
// vectorized execution are best-in-class for the high-cardinality
// append-only workloads NetSite produces (canary results, BGP UPDATEs,
// flow records). Postgres remains the right home for relational-config
// data (tenants, users, SLOs, annotations) where strong consistency
// and joins matter more than scan throughput. This split is architecture
// invariant A2 in CLAUDE.md.
//
// Why the native protocol (port 9000) over HTTP (port 8123): native
// protocol is faster, supports streaming inserts, and exposes server
// metadata (progress, profiling) the HTTP API does not. The trade-off
// is that loadbalancer/proxy support is thinner; for self-hosted
// deployments where NetSite talks to its own ClickHouse, this is fine.
package clickhouse
