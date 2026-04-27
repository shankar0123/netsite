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

package netql

import (
	"errors"
	"testing"
)

// kindAndValue is a compact (TokenKind, Value) pair for table-driven
// lexer tests; we don't assert positions in most cases because they
// fall out of the input shape and clutter the table.
type kindAndValue struct {
	Kind  TokenKind
	Value string
}

// lex helper: collects (kind, value) for every emitted Token EXCEPT
// the final TokEOF. Tests only care about the leading sequence.
func lexed(t *testing.T, input string) []kindAndValue {
	t.Helper()
	toks, err := Lex(input)
	if err != nil {
		t.Fatalf("Lex(%q): %v", input, err)
	}
	if toks[len(toks)-1].Kind != TokEOF {
		t.Fatalf("expected trailing TokEOF, got %v", toks[len(toks)-1])
	}
	out := make([]kindAndValue, 0, len(toks)-1)
	for _, tk := range toks[:len(toks)-1] {
		out = append(out, kindAndValue{Kind: tk.Kind, Value: tk.Value})
	}
	return out
}

// TestLex_CanonicalQuery walks the token sequence for the seven-word
// example from docs/algorithms/netql-language.md.
func TestLex_CanonicalQuery(t *testing.T) {
	got := lexed(t, "latency_p95 by pop where target = 'api.example.com' over 24h")
	want := []kindAndValue{
		{TokIdent, "latency_p95"},
		{TokKeyword, "by"},
		{TokIdent, "pop"},
		{TokKeyword, "where"},
		{TokIdent, "target"},
		{TokEq, "="},
		{TokString, "api.example.com"},
		{TokKeyword, "over"},
		{TokDuration, "24h"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d; want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestLex_CaseInsensitiveKeywords asserts that uppercase keywords
// and identifiers lower-case to the canonical form.
func TestLex_CaseInsensitiveKeywords(t *testing.T) {
	got := lexed(t, "Latency_P95 BY POP")
	want := []kindAndValue{
		{TokIdent, "latency_p95"},
		{TokKeyword, "by"},
		{TokIdent, "pop"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestLex_Operators walks the six comparison operators plus the
// keyword operators (in/contains/matches).
func TestLex_Operators(t *testing.T) {
	got := lexed(t, "x = 1 != 2 < 3 <= 4 > 5 >= 6 in (1) contains 'a' matches 'b'")
	want := []kindAndValue{
		{TokIdent, "x"},
		{TokEq, "="}, {TokInt, "1"},
		{TokNe, "!="}, {TokInt, "2"},
		{TokLt, "<"}, {TokInt, "3"},
		{TokLe, "<="}, {TokInt, "4"},
		{TokGt, ">"}, {TokInt, "5"},
		{TokGe, ">="}, {TokInt, "6"},
		{TokKeyword, "in"}, {TokLParen, "("}, {TokInt, "1"}, {TokRParen, ")"},
		{TokKeyword, "contains"}, {TokString, "a"},
		{TokKeyword, "matches"}, {TokString, "b"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d; want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestLex_NumbersAndDurations covers the integer/decimal/duration
// branch in lexNumberOrDuration.
func TestLex_NumbersAndDurations(t *testing.T) {
	got := lexed(t, "1 1.5 24h 7d 30m 60s 2w")
	want := []kindAndValue{
		{TokInt, "1"},
		{TokNumber, "1.5"},
		{TokDuration, "24h"},
		{TokDuration, "7d"},
		{TokDuration, "30m"},
		{TokDuration, "60s"},
		{TokDuration, "2w"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d; want %d (got=%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// TestLex_UnterminatedString surfaces as a *LexError.
func TestLex_UnterminatedString(t *testing.T) {
	_, err := Lex("x = 'oops")
	var le *LexError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v; want *LexError", err)
	}
	if le.Pos != 4 {
		t.Errorf("err.Pos = %d; want 4", le.Pos)
	}
}

// TestLex_BangWithoutEquals surfaces the dangling-bang case.
func TestLex_BangWithoutEquals(t *testing.T) {
	_, err := Lex("x ! 1")
	var le *LexError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v; want *LexError", err)
	}
}

// TestLex_UnknownCharacter rejects characters outside the grammar.
func TestLex_UnknownCharacter(t *testing.T) {
	_, err := Lex("x @ 1")
	var le *LexError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v; want *LexError", err)
	}
}

// TestLex_StringWithSpaces preserves whitespace inside string
// literals.
func TestLex_StringWithSpaces(t *testing.T) {
	got := lexed(t, "x = 'hello world'")
	if got[2].Value != "hello world" {
		t.Errorf("string value = %q; want %q", got[2].Value, "hello world")
	}
}

// TestTokenString covers the diagnostic stringer.
func TestTokenString(t *testing.T) {
	tok := Token{Kind: TokIdent, Value: "x", Pos: 5}
	if s := tok.String(); s == "" {
		t.Errorf("Token.String() empty")
	}
}
