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
	"fmt"
	"strings"
	"unicode"
)

// What: a small hand-rolled lexer that converts a netql source
// string into a slice of Tokens. No external scanner library; just a
// position cursor and a switch.
//
// How: single forward pass. Whitespace is skipped. Identifiers and
// keywords share an entry path because they look the same until we
// check the keyword set; durations look like numbers until we see a
// unit suffix. Operators (`!=`, `<=`, `>=`) need one byte of
// lookahead; everything else is single-byte.
//
// Why hand-rolled: the grammar is small (≤ 20 productions) and we
// want zero non-stdlib dependencies, fast lex, and good byte-offset
// error messages for the UI to underline. Generated lexers (lex,
// flex, antlr) optimise for grammars an order of magnitude larger
// than ours.

// Lex tokenises the input string. Returns a slice of Tokens that
// always ends with a TokEOF marker, or a *LexError at the first bad
// byte.
//
// All identifiers and keywords are lower-cased in the output —
// netql is case-insensitive on the surface, case-canonical in the
// AST.
func Lex(input string) ([]Token, error) {
	l := &lexer{src: input}
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		l.out = append(l.out, tok)
		if tok.Kind == TokEOF {
			return l.out, nil
		}
	}
}

type lexer struct {
	src string
	pos int
	out []Token
}

func (l *lexer) next() (Token, error) {
	l.skipWhitespace()
	if l.pos >= len(l.src) {
		return Token{Kind: TokEOF, Pos: l.pos}, nil
	}
	start := l.pos
	c := l.src[l.pos]
	switch {
	case c == '(':
		l.pos++
		return Token{Kind: TokLParen, Value: "(", Pos: start}, nil
	case c == ')':
		l.pos++
		return Token{Kind: TokRParen, Value: ")", Pos: start}, nil
	case c == ',':
		l.pos++
		return Token{Kind: TokComma, Value: ",", Pos: start}, nil
	case c == '\'':
		return l.lexString()
	case c == '=':
		l.pos++
		return Token{Kind: TokEq, Value: "=", Pos: start}, nil
	case c == '!':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.pos += 2
			return Token{Kind: TokNe, Value: "!=", Pos: start}, nil
		}
		return Token{}, &LexError{Pos: start, Msg: "expected '=' after '!'"}
	case c == '<':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.pos += 2
			return Token{Kind: TokLe, Value: "<=", Pos: start}, nil
		}
		l.pos++
		return Token{Kind: TokLt, Value: "<", Pos: start}, nil
	case c == '>':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
			l.pos += 2
			return Token{Kind: TokGe, Value: ">=", Pos: start}, nil
		}
		l.pos++
		return Token{Kind: TokGt, Value: ">", Pos: start}, nil
	case isDigit(c):
		return l.lexNumberOrDuration()
	case isIdentStart(c):
		return l.lexIdentOrKeyword()
	default:
		return Token{}, &LexError{Pos: start, Msg: fmt.Sprintf("unexpected character %q", c)}
	}
}

func (l *lexer) skipWhitespace() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.pos++
			continue
		}
		break
	}
}

// lexString consumes a single-quoted string literal. Embedded
// quotes are not supported in v0.0.9 — we picked simplicity over
// SQL-style doubled-quote escapes; if real queries need them we add
// `\\'` escaping and update the grammar in v0.0.10.
func (l *lexer) lexString() (Token, error) {
	start := l.pos
	l.pos++ // consume opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\'' {
			l.pos++ // consume closing quote
			return Token{Kind: TokString, Value: sb.String(), Pos: start}, nil
		}
		sb.WriteByte(c)
		l.pos++
	}
	return Token{}, &LexError{Pos: start, Msg: "unterminated string literal"}
}

// lexNumberOrDuration consumes digits, optionally with a decimal
// point, optionally followed by a duration unit (s/m/h/d/w). The
// unit decides whether the token is TokNumber, TokInt, or
// TokDuration.
//
// Why fold them into one function: the prefix is identical (digits
// optionally with a dot); branching on the suffix at the end keeps
// the cursor in one consistent state.
func (l *lexer) lexNumberOrDuration() (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}
	hasDot := false
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		hasDot = true
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
	}
	digits := l.src[start:l.pos]
	// Optional duration unit. Only valid if no decimal point — `1.5h`
	// is rejected because canary intervals are integer seconds and
	// fractional units would invite confusion (1.5h is 1h30m? 1h50m?).
	if !hasDot && l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case 's', 'm', 'h', 'd', 'w':
			l.pos++
			return Token{Kind: TokDuration, Value: digits + string(c), Pos: start}, nil
		}
	}
	if hasDot {
		return Token{Kind: TokNumber, Value: digits, Pos: start}, nil
	}
	return Token{Kind: TokInt, Value: digits, Pos: start}, nil
}

// lexIdentOrKeyword consumes letters/digits/underscores and then
// classifies as keyword vs identifier via the keyword map.
func (l *lexer) lexIdentOrKeyword() (Token, error) {
	start := l.pos
	for l.pos < len(l.src) && isIdentCont(l.src[l.pos]) {
		l.pos++
	}
	raw := l.src[start:l.pos]
	low := strings.ToLower(raw)
	if _, ok := keywords[low]; ok {
		return Token{Kind: TokKeyword, Value: low, Pos: start}, nil
	}
	return Token{Kind: TokIdent, Value: low, Pos: start}, nil
}

// isDigit / isIdentStart / isIdentCont are ASCII-only fast paths.
// netql identifiers are snake_case ASCII by convention; non-ASCII
// in identifiers would be a foot-gun for the column-aliasing logic
// in the translator and is rejected at the lex step.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isIdentStart(c byte) bool {
	if c == '_' {
		return true
	}
	r := rune(c)
	return unicode.IsLetter(r)
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}
