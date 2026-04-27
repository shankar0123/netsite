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

import (
	"errors"
	"fmt"
)

// TokenKind enumerates the lexer's output token classes.
type TokenKind int

// Canonical token kinds. The integer values are not part of the
// stable API; callers must use the named constants.
const (
	TokEOF      TokenKind = iota
	TokIdent              // identifier (lower-cased)
	TokString             // 'string literal'
	TokNumber             // numeric literal
	TokInt                // integer literal (digits only, no '.')
	TokDuration           // duration literal: 24h, 7d, 30m
	TokLParen             // (
	TokRParen             // )
	TokComma              // ,
	TokEq                 // =
	TokNe                 // !=
	TokLt                 // <
	TokLe                 // <=
	TokGt                 // >
	TokGe                 // >=
	// Keywords (canonical lower-case): the lexer emits TokKeyword
	// with the original keyword text in Value.
	TokKeyword
)

// Keyword set. Lookup is case-insensitive; the canonical form is
// lower-case and is what ends up in the AST. Kept as a map so the
// parser can do single O(1) keyword identification.
var keywords = map[string]struct{}{
	"by": {}, "where": {}, "and": {}, "or": {}, "not": {},
	"in": {}, "contains": {}, "matches": {},
	"over": {}, "step": {}, "order": {}, "asc": {}, "desc": {},
	"limit": {},
}

// Token is one lexer output unit.
type Token struct {
	Kind  TokenKind
	Value string // canonical form: identifiers/keywords lower-cased; strings without quotes
	Pos   int    // byte offset into the original input
}

// String renders a Token for diagnostic / debug output.
func (t Token) String() string {
	return fmt.Sprintf("%s(%q)@%d", tokenKindName(t.Kind), t.Value, t.Pos)
}

func tokenKindName(k TokenKind) string {
	switch k {
	case TokEOF:
		return "eof"
	case TokIdent:
		return "ident"
	case TokString:
		return "string"
	case TokNumber:
		return "number"
	case TokInt:
		return "int"
	case TokDuration:
		return "duration"
	case TokLParen:
		return "("
	case TokRParen:
		return ")"
	case TokComma:
		return ","
	case TokEq:
		return "="
	case TokNe:
		return "!="
	case TokLt:
		return "<"
	case TokLe:
		return "<="
	case TokGt:
		return ">"
	case TokGe:
		return ">="
	case TokKeyword:
		return "keyword"
	default:
		return "unknown"
	}
}

// Op is the predicate operator from the grammar.
type Op string

// Canonical op values. The string form matches the source token so
// we can round-trip a parsed AST back to the netql surface form.
const (
	OpEq       Op = "="
	OpNe       Op = "!="
	OpLt       Op = "<"
	OpLe       Op = "<="
	OpGt       Op = ">"
	OpGe       Op = ">="
	OpIn       Op = "in"
	OpContains Op = "contains"
	OpMatches  Op = "matches"
)

// ValueKind enumerates the literal kinds a predicate value can take.
type ValueKind int

// Canonical ValueKind values.
const (
	ValString ValueKind = iota
	ValNumber
	ValStringList
	ValNumberList
)

// Value is a typed predicate value (the right-hand side of a
// predicate). String / Number are scalar; StringList / NumberList
// drive the `in (a, b, c)` form.
type Value struct {
	Kind    ValueKind
	String  string
	Number  float64
	Strings []string
	Numbers []float64
}

// Predicate is the leaf of a filter expression.
type Predicate struct {
	Field string
	Op    Op
	Value Value
	Pos   int
}

// Expr is a node in the boolean expression tree. Exactly one of
// Predicate / And / Or / Not is non-nil.
//
// We keep a tiny three-shape sum-type rather than a richer AST
// hierarchy because expressions in netql are short — the type-check
// pass and the translator both walk the same shape, so any change
// here ripples to two visitors. Three shapes keep both small.
type Expr struct {
	Predicate *Predicate
	And       []*Expr
	Or        []*Expr
	Not       *Expr
	Pos       int
}

// Direction is the sort order for ORDER BY.
type Direction string

// Canonical Direction values. Default (when omitted) is DirAsc.
const (
	DirAsc  Direction = "asc"
	DirDesc Direction = "desc"
)

// Duration is a parsed duration literal (e.g., 24h, 7d).
//
// We carry the canonical (count, unit) pair rather than a
// time.Duration because the translator output uses ClickHouse's
// `INTERVAL N <UNIT>` form which speaks the same vocabulary. Going
// through time.Duration would lose the unit and force us to invent
// it back at translation time.
type Duration struct {
	Count int
	Unit  string // "s" | "m" | "h" | "d" | "w"
	Pos   int
}

// OrderBy is the optional `order by <col> [asc|desc]` clause.
type OrderBy struct {
	Field     string
	Direction Direction
	Pos       int
}

// Query is the top-level AST node — one parsed netql query.
//
// Every clause is optional except Metric. Pointers (vs zero-value
// structs) signal "absent" so the translator and type-checker can
// distinguish "user said nothing" from "user said the zero value".
type Query struct {
	Metric  string
	GroupBy []string
	Filter  *Expr     // optional
	Over    *Duration // optional
	Step    *Duration // optional
	OrderBy *OrderBy  // optional
	Limit   *int      // optional

	// Pos is the byte offset of the metric token — useful for
	// error reporting against the original input.
	Pos int
}

// LexError is returned by Lex when the input cannot be tokenised.
type LexError struct {
	Pos int
	Msg string
}

func (e *LexError) Error() string {
	return fmt.Sprintf("netql: lex error at byte %d: %s", e.Pos, e.Msg)
}

// ParseError is returned by Parse when the token sequence does not
// match the grammar.
type ParseError struct {
	Pos  int
	Want string
	Got  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("netql: parse error at byte %d: want %s, got %s", e.Pos, e.Want, e.Got)
}

// TypeError is returned by the type-checker (Check) when the AST
// references unknown metrics, ungroupable columns, or predicates
// against the wrong value type.
type TypeError struct {
	Reason string
}

func (e *TypeError) Error() string { return "netql: type error: " + e.Reason }

// TranslateError is returned by the translators when the AST cannot
// be lowered to the requested backend (e.g., a metric exists but
// has no ClickHouse implementation).
type TranslateError struct {
	Reason string
}

func (e *TranslateError) Error() string { return "netql: translate error: " + e.Reason }

// Sentinel error wrappers for callers that prefer errors.Is over
// type assertion.
var (
	ErrLex       = errors.New("netql: lex error")
	ErrParse     = errors.New("netql: parse error")
	ErrType      = errors.New("netql: type error")
	ErrTranslate = errors.New("netql: translate error")
)
