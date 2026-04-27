# netql — NetSite's Query Language

> Status: lexer + parser + AST + ClickHouse translator implemented in
> `pkg/netql/` (v0.0.9). PromQL translator + autocomplete land in
> v0.0.10. Corpus snapshot tests live in `pkg/netql/testdata/*.netql`.

## Problem statement

Operators want to ask questions like *"what's the p95 HTTP canary
latency for `api.example.com`, broken down by POP, over the last 24
hours?"* without learning two different query languages. The data is
split between two backends:

- **ClickHouse** stores high-cardinality time-series (canary results,
  flow records, BGP updates, PCAP-derived rows). Queries use
  ClickHouse SQL with `quantile`, `countIf`, `LowCardinality` columns.
- **Prometheus** stores low-cardinality service metrics scraped from
  the control plane (request counts, evaluator runs). Queries use
  PromQL.

Asking the same question in both languages produces two different
expressions that drift over time. Worse, the SQL or PromQL form
exposes implementation details (column names, label keys, function
arity) that aren't part of the operator's mental model.

netql is a small, English-shaped DSL that compiles to both targets.
The operator writes one query; netql translates to ClickHouse SQL or
PromQL based on the metric's home.

## Why a custom DSL and not just SQL/PromQL

The naive answer is "make operators write SQL." That fails three
ways:

1. **Two query languages, one mental model.** Splitting between SQL
   and PromQL doubles the cognitive load and makes it impossible to
   share queries across teams that prefer different tools.
2. **The good queries are short.** *"latency_p95 by pop over 24h"*
   compresses to seven words; the equivalent ClickHouse SQL is 80+
   characters of `quantile(0.95)(...)`, `groupArray`, `WHERE`, time
   bucketing. Operators don't write the long form repeatedly; they
   copy-paste a stale version.
3. **The "show me the SQL" affordance** (PRD D17 / OQ-04) is a
   teaching moment — netql is meant to be the on-ramp, not the
   ceiling. We compile to inspectable SQL/PromQL so operators can
   read what runs and graduate to the underlying language for
   advanced cases.

We considered using Cuelang or PromQL itself with a thin wrapper.
Both fail the "compresses cleanly to seven words" test. We also
rejected a parser-generator (ANTLR, goyacc, participle) — netql's
grammar is small enough that a hand-rolled recursive-descent parser
is shorter than any generated alternative, gives us the best error
messages, and ships with zero non-stdlib dependencies. **Decided OQ-
04 → hand-rolled.**

## Surface — the seven-word query

```
latency_p95 by pop where target = 'api.example.com' over 24h
```

Parses to: *over the last 24 hours, group canary HTTP results by
POP and report the 95th-percentile latency for the target
`api.example.com`.* Translates to ClickHouse:

```sql
SELECT
  pop_id AS pop,
  quantile(0.95)(latency_ms) AS latency_p95
FROM canary_results
WHERE observed_at >= now() - INTERVAL 24 HOUR
  AND target = 'api.example.com'
  AND tenant_id = $1                 -- tenant scoping injected
GROUP BY pop_id
ORDER BY pop ASC
```

Tenant scoping is **always** injected by the translator. It is
impossible to write a netql query that escapes the caller's tenant.

## EBNF — the grammar (locked v0.0.9)

```ebnf
query       = metric [ groupby ] [ filter ] [ over ] [ step ] [ orderby ] [ limit ] ;

metric      = identifier ;       (* e.g., success_rate, latency_p95, count *)

groupby     = "by" identifier { "," identifier } ;

filter      = "where" expr ;

expr        = and_expr { "or" and_expr } ;
and_expr    = unary { "and" unary } ;
unary       = [ "not" ] term ;
term        = predicate
            | "(" expr ")" ;

predicate   = identifier op value ;
op          = "=" | "!=" | "<" | "<=" | ">" | ">=" | "in" | "contains" | "matches" ;
value       = string
            | number
            | "(" string { "," string } ")"
            | "(" number { "," number } ")" ;

over        = "over" duration ;
step        = "step" duration ;
orderby     = "order" "by" identifier [ direction ] ;
direction   = "asc" | "desc" ;
limit       = "limit" int ;

duration    = digit { digit } unit ;
unit        = "s" | "m" | "h" | "d" | "w" ;

identifier  = letter { letter | digit | "_" } ;
string      = "'" { any-char-except-quote } "'" ;
number      = digit { digit } [ "." digit { digit } ] ;
int         = digit { digit } ;
```

Reserved keywords (case-insensitive at the lexer; canonical
lower-case in the AST): `by`, `where`, `and`, `or`, `not`, `in`,
`contains`, `matches`, `over`, `step`, `order`, `asc`, `desc`,
`limit`. Identifiers use snake_case; the parser lowercases on
ingest so `latency_p95`, `Latency_P95`, and `LATENCY_P95` are the
same metric.

## Type system

Every metric has:

- A **home backend** (`clickhouse` or `prometheus`).
- A **value type** (`numeric`, `ratio`, `count`, `duration_ms`).
- A **groupable-by** set (the columns it can aggregate over).
- A **filterable-by** set with each column's value type.

The type system rejects nonsensical queries before translation. For
example, `latency_p95 by error_kind` is OK (POP-by-error_kind
breakdown), but `latency_p95 by tenant_id` is rejected because
tenant scoping is injected and never user-controlled.

For v0.0.9 the registry holds three metrics, all sourced from the
ClickHouse `canary_results` table:

| Metric         | Backend    | Value         | Group-by                  | Filter columns                                                |
|----------------|------------|---------------|----------------------------|----------------------------------------------------------------|
| `success_rate` | clickhouse | ratio (0–1)   | pop, target, kind, error_kind | pop, target, kind, error_kind, observed_at                     |
| `latency_p95`  | clickhouse | duration_ms   | pop, target, kind          | pop, target, kind, observed_at                                 |
| `count`        | clickhouse | count         | pop, target, kind, error_kind | pop, target, kind, error_kind, observed_at                     |

The registry expands as new ClickHouse tables ship (BGP updates in
Phase 1, flow records in Phase 3, etc.).

## ClickHouse translation rules (v0.0.9)

- **`success_rate`** → `countIf(error_kind = '') / count(*)`. The
  ratio is computed inside ClickHouse, not transported as two
  columns. Operators get one number per group.
- **`latency_p95`** → `quantile(0.95)(latency_ms)`. ClickHouse's
  `quantile` is a t-digest approximation; it's fast and usually
  accurate to within ~1 ms for our SLI ranges. We document this in
  the metric's tooltip when the UI lands.
- **`count`** → `count(*)`.
- **`by` columns** → `GROUP BY <col>` plus `<col> AS <alias>` in the
  SELECT list. Aliases are the netql identifier (so `pop` in netql
  → `pop_id AS pop` in SQL).
- **`where`** → SQL `WHERE` clause; predicates compose with `AND`/
  `OR`/`NOT` in the natural way. String values are parameterised
  with `$N` placeholders; numbers are inlined.
- **`over Nh`** → `WHERE observed_at >= now() - INTERVAL N HOUR`.
- **`order by <col>`** without direction defaults to `ASC`.
- **`limit N`** with no explicit limit defaults to a hard cap of 10
  000 rows in the translator (DoS guard).
- **Tenant scoping** → always-injected `AND tenant_id = $1` (every
  query). The translator is the only place that knows the tenant
  ID; callers cannot bypass it.
- **Parameter list** is returned alongside the SQL string. The
  caller binds them at execution time (we never string-format user
  input into the SQL).

## Calibration / verification

- **Corpus snapshot tests.** `pkg/netql/testdata/*.netql` holds a
  growing set of canonical queries. Each `.netql` file pairs with a
  `.ch.sql` snapshot of the expected ClickHouse output. The test
  runner re-translates and diffs; intentional output changes
  require updating the snapshot in the same commit.
- **Round-trip equivalence on test data.** Phase 1 plan: spin up a
  testcontainer ClickHouse, seed `canary_results` with synthetic
  rows, run each corpus query through netql + translation, and
  assert the result matches an independently-computed expectation.
- **EBNF stability.** Once the grammar is locked, breaking changes
  go through a deprecation cycle: parse old form, emit a warning,
  document removal in PROJECT_STATE.md §15, remove in next minor
  release.

## Failure modes

- **Lex error** (unterminated string, unknown character) → `LexError{Pos, Msg}`.
- **Parse error** (unexpected token, missing keyword) → `ParseError{Pos, Want, Got}`.
- **Type error** (unknown metric, ungroupable column, unfilterable
  column, wrong value type for predicate) → `TypeError{Reason}`.
- **Translation error** (metric not implemented for the requested
  backend, unsupported operation against this metric's filter set)
  → `TranslateError{Reason}`.

Errors carry the original token position so the UI can underline.
For v0.0.9 we expose `Pos` as a byte offset; the UI converts to
line/column on display.

## Why we picked these defaults

- **Default over** (when omitted) is **1h**. Most operator
  questions are "what's happening right now"; an hour of canary
  data is enough to see one full SLO eval cycle.
- **Default order** is `ASC` so `by` columns surface alphabetically
  by default — predictable, no surprise data shifts when groups are
  added.
- **Default limit** is 10 000 — enough for any human-readable table
  on the dashboard, low enough that a runaway query doesn't memory-
  bomb the API.

## What's deferred to later versions

- **PromQL translator** (v0.0.10). Same AST; second visitor.
- **Autocomplete** (v0.0.10). Returns possible identifiers / values
  given a partial token at a given byte position. The UI binds it
  to a debounced editor input.
- **Joins across backends** (Phase 1). For now, each metric has a
  single backend.
- **Window functions / sub-queries** (Phase 2). `over` and `step`
  cover the common time-window cases.
- **`as` aliasing** (Phase 1). `latency_p95 as p95 by pop` reads
  natural; we ship without it because the column alias is already
  the metric name.
- **`since` and `between`** (Phase 2). For absolute time bounds.
  Most operator questions are relative-time; `over Nh` covers them.

## Prior art consulted

- **PromQL** (Prometheus) — the gold standard for label-based
  metric queries. Borrowed the time-range duration syntax (`24h`,
  `7d`) and the offset-from-now mental model.
- **Loki LogQL** (Grafana) — a friendly DSL over a different
  backend (logs). Validated the "small DSL compiles to one or more
  backends" pattern.
- **Honeycomb's BubbleUp** and **Datadog's query editor** — the
  in-product editors that operators are coming from. We mimic the
  *grammar shape* (metric, group, filter, range) so the migration
  cost is low.

## References

- Hyndman & Athanasopoulos, *Forecasting: Principles and Practice*,
  3rd ed. — for the time-series mental model that informs the
  default windows.
- Aho, Sethi, Ullman, *Compilers: Principles, Techniques, and
  Tools*, 2nd ed. — for the recursive-descent parser shape.
- Brian W. Kernighan, "An Introduction to Awk" — for the value of
  *one small language done well*.
