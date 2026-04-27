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
	"strconv"
)

// What: a hand-rolled recursive-descent parser for the netql
// grammar locked in docs/algorithms/netql-language.md. The parser
// consumes a flat token stream and emits a Query AST (or
// *ParseError). One function per non-terminal.
//
// How: standard recursive-descent shape — `peek`/`expect`/`accept`
// helpers, one method per grammar production, no backtracking
// because the grammar is LL(1). Operator precedence in the boolean
// expression: `or` lowest, `and` middle, `not` highest, predicate
// leaves at the bottom. Implemented by climbing through expr →
// and_expr → unary → term.
//
// Why hand-rolled: the grammar is small, error messages are
// precious in an in-product editor, and we get to keep zero parser
// dependencies in the dependency graph (acquisition diligence
// cleanliness).

// Parse turns a token slice (from Lex) into a Query AST. Returns
// *ParseError with byte-offset position on failure.
func Parse(input string) (*Query, error) {
	toks, err := Lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	q, err := p.parseQuery()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != TokEOF {
		t := p.peek()
		return nil, &ParseError{Pos: t.Pos, Want: "end of input", Got: t.String()}
	}
	return q, nil
}

type parser struct {
	toks []Token
	pos  int
}

func (p *parser) peek() Token { return p.toks[p.pos] }

func (p *parser) advance() Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

// accept returns the next token and advances iff it matches one of
// the requested kinds and (optionally) one of the requested values.
// values may be empty to accept any value of the matching kind.
func (p *parser) accept(kind TokenKind, values ...string) (Token, bool) {
	t := p.peek()
	if t.Kind != kind {
		return Token{}, false
	}
	if len(values) > 0 {
		ok := false
		for _, v := range values {
			if t.Value == v {
				ok = true
				break
			}
		}
		if !ok {
			return Token{}, false
		}
	}
	return p.advance(), true
}

// expect is accept-with-error-on-miss.
func (p *parser) expect(kind TokenKind, want string, values ...string) (Token, error) {
	t, ok := p.accept(kind, values...)
	if !ok {
		got := p.peek()
		return Token{}, &ParseError{Pos: got.Pos, Want: want, Got: got.String()}
	}
	return t, nil
}

// parseQuery is the top-level production:
//
//	query = metric [ groupby ] [ filter ] [ over ] [ step ] [ orderby ] [ limit ] ;
func (p *parser) parseQuery() (*Query, error) {
	mt, err := p.expect(TokIdent, "metric identifier")
	if err != nil {
		return nil, err
	}
	q := &Query{Metric: mt.Value, Pos: mt.Pos}

	// Each clause is keyword-prefixed and order-fixed per the EBNF;
	// we parse them in order, peeking to decide whether each is
	// present.
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "by" {
		gb, err := p.parseGroupBy()
		if err != nil {
			return nil, err
		}
		q.GroupBy = gb
	}
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "where" {
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		q.Filter = expr
	}
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "over" {
		p.advance()
		dt, err := p.expect(TokDuration, "duration after 'over'")
		if err != nil {
			return nil, err
		}
		d, err := parseDurationToken(dt)
		if err != nil {
			return nil, err
		}
		q.Over = d
	}
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "step" {
		p.advance()
		dt, err := p.expect(TokDuration, "duration after 'step'")
		if err != nil {
			return nil, err
		}
		d, err := parseDurationToken(dt)
		if err != nil {
			return nil, err
		}
		q.Step = d
	}
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "order" {
		ob, err := p.parseOrderBy()
		if err != nil {
			return nil, err
		}
		q.OrderBy = ob
	}
	if t := p.peek(); t.Kind == TokKeyword && t.Value == "limit" {
		p.advance()
		nt, err := p.expect(TokInt, "integer after 'limit'")
		if err != nil {
			return nil, err
		}
		n, perr := strconv.Atoi(nt.Value)
		if perr != nil {
			return nil, &ParseError{Pos: nt.Pos, Want: "valid integer", Got: nt.Value}
		}
		q.Limit = &n
	}
	return q, nil
}

func (p *parser) parseGroupBy() ([]string, error) {
	if _, err := p.expect(TokKeyword, "'by'", "by"); err != nil {
		return nil, err
	}
	first, err := p.expect(TokIdent, "identifier after 'by'")
	if err != nil {
		return nil, err
	}
	out := []string{first.Value}
	for {
		if _, ok := p.accept(TokComma); !ok {
			break
		}
		nxt, err := p.expect(TokIdent, "identifier after ','")
		if err != nil {
			return nil, err
		}
		out = append(out, nxt.Value)
	}
	return out, nil
}

func (p *parser) parseOrderBy() (*OrderBy, error) {
	if _, err := p.expect(TokKeyword, "'order'", "order"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokKeyword, "'by'", "by"); err != nil {
		return nil, err
	}
	id, err := p.expect(TokIdent, "identifier after 'order by'")
	if err != nil {
		return nil, err
	}
	ob := &OrderBy{Field: id.Value, Direction: DirAsc, Pos: id.Pos}
	if t, ok := p.accept(TokKeyword, "asc", "desc"); ok {
		ob.Direction = Direction(t.Value)
	}
	return ob, nil
}

// parseExpr → and_expr ("or" and_expr)*
func (p *parser) parseExpr() (*Expr, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != TokKeyword || p.peek().Value != "or" {
		return left, nil
	}
	terms := []*Expr{left}
	for {
		t, ok := p.accept(TokKeyword, "or")
		if !ok {
			break
		}
		next, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
		_ = t
	}
	return &Expr{Or: terms, Pos: left.Pos}, nil
}

// parseAndExpr → unary ("and" unary)*
func (p *parser) parseAndExpr() (*Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != TokKeyword || p.peek().Value != "and" {
		return left, nil
	}
	terms := []*Expr{left}
	for {
		_, ok := p.accept(TokKeyword, "and")
		if !ok {
			break
		}
		next, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
	}
	return &Expr{And: terms, Pos: left.Pos}, nil
}

// parseUnary → "not" term | term
func (p *parser) parseUnary() (*Expr, error) {
	if t, ok := p.accept(TokKeyword, "not"); ok {
		inner, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		return &Expr{Not: inner, Pos: t.Pos}, nil
	}
	return p.parseTerm()
}

// parseTerm → predicate | "(" expr ")"
func (p *parser) parseTerm() (*Expr, error) {
	if _, ok := p.accept(TokLParen); ok {
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokRParen, "')' to close grouped expression"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	pred, err := p.parsePredicate()
	if err != nil {
		return nil, err
	}
	return &Expr{Predicate: pred, Pos: pred.Pos}, nil
}

// parsePredicate → identifier op value
func (p *parser) parsePredicate() (*Predicate, error) {
	id, err := p.expect(TokIdent, "predicate field identifier")
	if err != nil {
		return nil, err
	}
	op, err := p.parseOp()
	if err != nil {
		return nil, err
	}
	val, err := p.parseValue(op)
	if err != nil {
		return nil, err
	}
	return &Predicate{Field: id.Value, Op: op, Value: val, Pos: id.Pos}, nil
}

func (p *parser) parseOp() (Op, error) {
	t := p.peek()
	switch t.Kind {
	case TokEq:
		p.advance()
		return OpEq, nil
	case TokNe:
		p.advance()
		return OpNe, nil
	case TokLt:
		p.advance()
		return OpLt, nil
	case TokLe:
		p.advance()
		return OpLe, nil
	case TokGt:
		p.advance()
		return OpGt, nil
	case TokGe:
		p.advance()
		return OpGe, nil
	case TokKeyword:
		switch t.Value {
		case "in":
			p.advance()
			return OpIn, nil
		case "contains":
			p.advance()
			return OpContains, nil
		case "matches":
			p.advance()
			return OpMatches, nil
		}
	}
	return "", &ParseError{Pos: t.Pos, Want: "predicate operator (=, !=, <, <=, >, >=, in, contains, matches)", Got: t.String()}
}

// parseValue is shaped by the operator: `in` requires a list,
// `contains` and `matches` require a string, comparisons accept
// scalar string or number.
func (p *parser) parseValue(op Op) (Value, error) {
	if op == OpIn {
		return p.parseValueList()
	}
	t := p.peek()
	switch t.Kind {
	case TokString:
		p.advance()
		return Value{Kind: ValString, String: t.Value}, nil
	case TokNumber, TokInt:
		p.advance()
		f, perr := strconv.ParseFloat(t.Value, 64)
		if perr != nil {
			return Value{}, &ParseError{Pos: t.Pos, Want: "valid number", Got: t.Value}
		}
		return Value{Kind: ValNumber, Number: f}, nil
	}
	return Value{}, &ParseError{Pos: t.Pos, Want: "string or number value", Got: t.String()}
}

// parseValueList → "(" string {"," string} ")" | "(" number {"," number} ")"
//
// We commit to one element type from the first item and require all
// following items to match. Mixed lists are a foot-gun (operators
// rarely want them; type-checking the predicate downstream gets
// muddled by mixed-type column comparisons).
func (p *parser) parseValueList() (Value, error) {
	if _, err := p.expect(TokLParen, "'(' to start list"); err != nil {
		return Value{}, err
	}
	first := p.peek()
	switch first.Kind {
	case TokString:
		strs, err := p.parseStringList()
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: ValStringList, Strings: strs}, nil
	case TokNumber, TokInt:
		nums, err := p.parseNumberList()
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: ValNumberList, Numbers: nums}, nil
	}
	return Value{}, &ParseError{Pos: first.Pos, Want: "string or number in list", Got: first.String()}
}

func (p *parser) parseStringList() ([]string, error) {
	first, err := p.expect(TokString, "string in list")
	if err != nil {
		return nil, err
	}
	out := []string{first.Value}
	for {
		if _, ok := p.accept(TokComma); !ok {
			break
		}
		nxt, err := p.expect(TokString, "string after ','")
		if err != nil {
			return nil, err
		}
		out = append(out, nxt.Value)
	}
	if _, err := p.expect(TokRParen, "')' to close list"); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *parser) parseNumberList() ([]float64, error) {
	first := p.advance() // we already peeked a number
	f, perr := strconv.ParseFloat(first.Value, 64)
	if perr != nil {
		return nil, &ParseError{Pos: first.Pos, Want: "valid number", Got: first.Value}
	}
	out := []float64{f}
	for {
		if _, ok := p.accept(TokComma); !ok {
			break
		}
		t := p.peek()
		if t.Kind != TokNumber && t.Kind != TokInt {
			return nil, &ParseError{Pos: t.Pos, Want: "number after ','", Got: t.String()}
		}
		p.advance()
		f, perr := strconv.ParseFloat(t.Value, 64)
		if perr != nil {
			return nil, &ParseError{Pos: t.Pos, Want: "valid number", Got: t.Value}
		}
		out = append(out, f)
	}
	if _, err := p.expect(TokRParen, "')' to close list"); err != nil {
		return nil, err
	}
	return out, nil
}

// parseDurationToken splits a TokDuration value (e.g., "24h") into
// its (count, unit) parts. The lexer already validated the unit
// character, so the only failure mode is a non-numeric prefix —
// which the lexer also prevents — making this a pure structural
// split.
func parseDurationToken(t Token) (*Duration, error) {
	v := t.Value
	if len(v) < 2 {
		return nil, &ParseError{Pos: t.Pos, Want: "duration like 24h", Got: v}
	}
	unit := v[len(v)-1:]
	digits := v[:len(v)-1]
	n, err := strconv.Atoi(digits)
	if err != nil {
		return nil, &ParseError{Pos: t.Pos, Want: "duration count integer", Got: digits}
	}
	if n <= 0 {
		return nil, &ParseError{Pos: t.Pos, Want: "duration count > 0", Got: digits}
	}
	return &Duration{Count: n, Unit: unit, Pos: t.Pos}, nil
}
