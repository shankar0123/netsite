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

// Package netql is NetSite's small, English-shaped query language.
// It compiles to ClickHouse SQL (and PromQL, in v0.0.10+) so
// operators write one query and get the right backend automatically.
//
// What:
//   - A hand-rolled recursive-descent lexer + parser for a tiny DSL
//     of the form `metric [by ...] [where ...] [over ...] [step ...]
//     [order by ...] [limit ...]`.
//   - A type system that validates a query against a metric registry
//     before translation — unknown metric, ungroupable column,
//     wrong-type predicate value all surface as TypeError.
//   - A ClickHouse translator that produces parameterised SQL with
//     tenant scoping always injected.
//
// How: tokens are produced by Lex; Parse consumes them into a Query
// AST; the AST is type-checked against a Registry; the Translator
// walks the AST and emits backend-specific output.
//
// Why: see docs/algorithms/netql-language.md. In one sentence —
// operators want one short query language, the data lives in two
// stores, and we want the translation to be inspectable so the DSL
// is an on-ramp to ClickHouse/Prometheus, not a ceiling.
package netql
