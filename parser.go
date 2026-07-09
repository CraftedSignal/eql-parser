package eql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxInputSize is the largest query Parse and ExtractConditions accept.
const MaxInputSize = 1 << 20 // 1 MiB

// maxExprDepth bounds expression nesting to keep hostile inputs from
// exhausting the stack.
const maxExprDepth = 200

// ErrEmptyQuery is returned when the input contains no tokens.
var ErrEmptyQuery = errors.New("empty query")

// ErrInputTooLarge is returned when the input exceeds MaxInputSize.
var ErrInputTooLarge = errors.New("input exceeds maximum size")

// Parse parses a single EQL statement and returns its AST. The parser is
// tolerant: it recovers from many errors and still produces a partial AST,
// but any problem encountered is reported in the returned error (the AST is
// still valid to inspect). Input is parsed as-is; use NormalizeQuery first
// for content pasted from documents.
func Parse(query string) (*Query, error) {
	q, errs := parseTolerant(query)
	if len(errs) > 0 {
		return q, errors.New(strings.Join(errs, "; "))
	}
	return q, nil
}

// ParseExpression parses a standalone boolean expression (no event category,
// sequences, or pipes).
func ParseExpression(input string) (Expr, error) {
	if len(input) > MaxInputSize {
		return nil, ErrInputTooLarge
	}
	lex := newLexer(input)
	p := &parser{toks: lex.tokens, errs: lex.errs}
	if p.cur().Type == TokenEOF {
		return nil, ErrEmptyQuery
	}
	e := p.parseExpression()
	if p.cur().Type != TokenEOF {
		p.errorAt(p.cur(), "unexpected trailing input")
	}
	if len(p.errs) > 0 {
		return e, errors.New(strings.Join(p.errs, "; "))
	}
	return e, nil
}

// parseTolerant runs the parser collecting every error instead of failing.
func parseTolerant(query string) (*Query, []string) {
	if len(query) > MaxInputSize {
		return &Query{}, []string{ErrInputTooLarge.Error()}
	}
	lex := newLexer(query)
	p := &parser{toks: lex.tokens, errs: lex.errs}
	if p.cur().Type == TokenEOF {
		return &Query{}, []string{ErrEmptyQuery.Error()}
	}
	q := p.parseStatement()
	return q, p.errs
}

type parser struct {
	toks    []Token
	pos     int
	errs    []string
	depth   int
	tooDeep bool
}

func (p *parser) cur() Token {
	// pos is maintained in [0, len(toks)-1]: next() never advances past the
	// final EOF token, so this index is always valid.
	return p.toks[p.pos]
}

func (p *parser) peekType(offset int) TokenType {
	i := p.pos + offset
	if i < len(p.toks) {
		return p.toks[i].Type
	}
	return TokenEOF
}

func (p *parser) next() Token {
	t := p.cur()
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) accept(typ TokenType) bool {
	if p.cur().Type == typ {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(typ TokenType) (Token, bool) {
	if p.cur().Type == typ {
		return p.next(), true
	}
	p.errorAt(p.cur(), "expected %s, found %s", typ, describeToken(p.cur()))
	return p.cur(), false
}

func (p *parser) errorAt(t Token, format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf("line %d:%d: %s", t.Line, t.Col, fmt.Sprintf(format, args...)))
}

func describeToken(t Token) string {
	switch t.Type {
	case TokenEOF:
		return "end of query"
	case TokenIdent, TokenNumber:
		return fmt.Sprintf("%q", t.Text)
	case TokenString:
		return "string"
	default:
		return fmt.Sprintf("%q", t.Type.String())
	}
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (p *parser) parseStatement() *Query {
	q := &Query{}
	switch p.cur().Type {
	case TokenSequence:
		q.Body = p.parseSequence(KindSequence)
	case TokenJoin:
		q.Body = p.parseSequence(KindJoin)
	case TokenSample:
		q.Body = p.parseSequence(KindSample)
	default:
		q.Body = p.parseEventQuery(false)
	}
	q.Pipes = p.parsePipes()
	if p.tooDeep {
		// Deep-nesting bailout: discard the remainder.
		p.pos = len(p.toks) - 1
	}
	if p.cur().Type != TokenEOF {
		p.errorAt(p.cur(), "unexpected trailing input starting at %s", describeToken(p.cur()))
	}
	return q
}

// parseEventQuery parses `category where expr`, `any where expr`, or (when
// tolerated) a bare boolean expression. inBrackets marks sequence-term and
// lineage contexts, where a category is normally mandatory.
func (p *parser) parseEventQuery(inBrackets bool) *EventQuery {
	e := &EventQuery{}

	// Category form requires `where` as the second token.
	switch p.cur().Type {
	case TokenAny:
		if p.peekType(1) == TokenWhere {
			p.next()
			p.next()
			e.CategoryAny = true
			e.Where = p.parseExpression()
			return e
		}
	case TokenIdent, TokenString, TokenBacktickIdent:
		if p.peekType(1) == TokenWhere {
			t := p.next()
			p.next() // where
			if t.Type == TokenIdent {
				e.Category = t.Text
			} else {
				e.Category = t.Value
			}
			e.Where = p.parseExpression()
			return e
		}
	case TokenWhere:
		// `where expr` with a missing category: tolerate.
		p.errorAt(p.cur(), "missing event category before 'where'")
		p.next()
		e.CategoryAny = true
		e.Where = p.parseExpression()
		return e
	}

	// Bare expression form (extension for stored rule fragments).
	if inBrackets {
		p.errorAt(p.cur(), "expected 'category where condition' inside brackets")
	}
	e.Bare = true
	e.Where = p.parseExpression()
	return e
}

// parseSequence parses sequence/join/sample constructs, which share a shape.
func (p *parser) parseSequence(kind SequenceKind) *Sequence {
	s := &Sequence{Kind: kind}
	p.next() // sequence/join/sample keyword

	// Header: `by ... [with maxspan=...]` or `with maxspan=... [by ...]`.
	if p.cur().Type == TokenBy {
		s.By = p.parseByKeys()
		if p.cur().Type == TokenWith {
			s.MaxSpan = p.parseWithMaxspan(kind)
		}
	} else if p.cur().Type == TokenWith {
		s.MaxSpan = p.parseWithMaxspan(kind)
		if p.cur().Type == TokenBy {
			s.WithByOrder = true
			s.By = p.parseByKeys()
		}
	}

	// Terms.
	for {
		if p.tooDeep {
			return s
		}
		t := p.cur().Type
		if t != TokenLBrack && t != TokenMissing {
			break
		}
		// cur is '[' or '![', so parseSequenceStep always returns a step here.
		s.Steps = append(s.Steps, p.parseSequenceStep(kind))
	}

	if p.accept(TokenUntil) {
		if kind == KindSample {
			p.errorAt(p.cur(), "sample does not support until")
		}
		s.Until = p.parseSequenceStep(kind)
	}

	p.validateSequence(s)
	return s
}

func (p *parser) validateSequence(s *Sequence) {
	positive := 0
	missing := 0
	for _, st := range s.Steps {
		if st.Missing {
			missing++
		} else {
			positive++
		}
	}
	switch s.Kind {
	case KindSequence:
		// A single step repeated with `with runs=N` (N>=2) is a valid
		// multi-event sequence — the term matches N consecutive times.
		singleWithRuns := len(s.Steps) == 1 && s.Steps[0].Runs >= 2
		if len(s.Steps) < 2 && !singleWithRuns {
			p.errs = append(p.errs, "sequence requires at least two events")
		}
		if missing > 0 {
			if positive == 0 {
				p.errs = append(p.errs, "sequence with missing events requires at least one positive event")
			}
			if s.MaxSpan == "" {
				p.errs = append(p.errs, "sequence with missing events requires 'with maxspan'")
			}
		}
	case KindJoin:
		if len(s.Steps) < 2 {
			p.errs = append(p.errs, "join requires at least two events")
		}
	case KindSample:
		if len(s.Steps) < 2 {
			p.errs = append(p.errs, "sample requires at least two events")
		}
		if len(s.By) == 0 {
			hasStepBy := false
			for _, st := range s.Steps {
				if len(st.By) > 0 {
					hasStepBy = true
					break
				}
			}
			if !hasStepBy {
				p.errs = append(p.errs, "sample requires join keys ('by')")
			}
		}
	}

	// Join-key arity must be consistent across steps.
	if len(s.Steps) > 1 {
		first := len(s.By) + len(s.Steps[0].By)
		for _, st := range s.Steps[1:] {
			if len(s.By)+len(st.By) != first {
				p.errs = append(p.errs, fmt.Sprintf("inconsistent join key count across %s events", s.Kind))
				break
			}
		}
	}
}

func (p *parser) parseSequenceStep(kind SequenceKind) *SequenceStep {
	st := &SequenceStep{}
	switch p.cur().Type {
	case TokenMissing:
		st.Missing = true
		if kind != KindSequence {
			p.errorAt(p.cur(), "missing events (![...]) are only supported in sequence")
		}
		p.next()
	case TokenLBrack:
		p.next()
	default:
		p.errorAt(p.cur(), "expected '[' to open a %s event", kind)
		return nil
	}

	st.Query = p.parseEventQuery(true)
	if _, ok := p.expect(TokenRBrack); !ok {
		// Recovery: skip to the next plausible boundary.
		p.skipTo(TokenRBrack, TokenLBrack, TokenMissing, TokenUntil, TokenPipe)
		p.accept(TokenRBrack)
	}

	if p.cur().Type == TokenBy {
		st.By = p.parseByKeys()
	}

	// `with runs=N` (sequence only; the grammar allows any key, ES accepts
	// only "runs").
	if p.cur().Type == TokenWith && p.peekType(1) != TokenMaxspan {
		p.next()
		keyTok := p.cur()
		if keyTok.Type != TokenIdent {
			p.errorAt(keyTok, "expected modifier name after 'with'")
			return st
		}
		p.next()
		st.WithKey = keyTok.Value
		if !strings.EqualFold(keyTok.Value, "runs") {
			p.errorAt(keyTok, "unknown event modifier %q (expected 'runs')", keyTok.Value)
		}
		if _, ok := p.expect(TokenAssign); !ok {
			return st
		}
		numTok, ok := p.expect(TokenNumber)
		if !ok {
			return st
		}
		n, err := strconv.Atoi(numTok.Text)
		if err != nil {
			p.errorAt(numTok, "runs value must be an integer")
			return st
		}
		st.Runs = n
		if strings.EqualFold(keyTok.Value, "runs") && (n < 1 || n > 100) {
			p.errorAt(numTok, "runs value must be between 1 and 100")
		}
		if st.Missing {
			p.errorAt(keyTok, "missing events do not support 'with runs'")
		}
	}
	return st
}

func (p *parser) parseByKeys() []Expr {
	p.next() // by
	var keys []Expr
	for {
		if p.tooDeep {
			return keys
		}
		keys = append(keys, p.parseExpression())
		if !p.accept(TokenComma) {
			break
		}
	}
	return keys
}

func (p *parser) parseWithMaxspan(kind SequenceKind) string {
	p.next() // with
	if kind != KindSequence {
		p.errorAt(p.cur(), "%s does not support maxspan", kind)
	}
	if _, ok := p.expect(TokenMaxspan); !ok {
		p.skipTo(TokenLBrack, TokenMissing, TokenBy, TokenPipe)
		return ""
	}
	if _, ok := p.expect(TokenAssign); !ok {
		return ""
	}
	numTok, ok := p.expect(TokenNumber)
	if !ok {
		return ""
	}
	span := numTok.Text
	// Optional time unit written as a trailing identifier (30s lexes as
	// number 30 + identifier s).
	if p.cur().Type == TokenIdent && isTimeUnit(p.cur().Value) {
		span += p.next().Value
	} else if p.cur().Type == TokenIdent {
		p.errorAt(p.cur(), "unknown time unit %q", p.cur().Value)
		p.next()
	} else {
		p.errorAt(numTok, "maxspan duration requires a time unit (e.g. 30s)")
	}
	return span
}

func isTimeUnit(s string) bool {
	switch strings.ToLower(s) {
	case "ms", "s", "m", "h", "d", "micros", "nanos",
		"sec", "secs", "second", "seconds",
		"min", "mins", "minute", "minutes",
		"hour", "hours", "day", "days":
		return true
	}
	return false
}

// MaxSpanDuration converts a maxspan value like "30s" or "2h" to
// milliseconds. Returns false when the text is not a recognized duration.
func MaxSpanDuration(span string) (int64, bool) {
	if span == "" {
		return 0, false
	}
	i := 0
	for i < len(span) && (span[i] >= '0' && span[i] <= '9' || span[i] == '.') {
		i++
	}
	num, err := strconv.ParseFloat(span[:i], 64)
	if err != nil {
		return 0, false
	}
	var mult float64
	switch strings.ToLower(span[i:]) {
	case "ms":
		mult = 1
	case "micros":
		mult = 0.001
	case "nanos":
		mult = 0.000001
	case "s", "sec", "secs", "second", "seconds", "":
		mult = 1000
	case "m", "min", "mins", "minute", "minutes":
		mult = 60 * 1000
	case "h", "hour", "hours":
		mult = 60 * 60 * 1000
	case "d", "day", "days":
		mult = 24 * 60 * 60 * 1000
	default:
		return 0, false
	}
	return int64(num * mult), true
}

func (p *parser) parsePipes() []*Pipe {
	var pipes []*Pipe
	for p.accept(TokenPipe) {
		if p.tooDeep {
			return pipes
		}
		nameTok := p.cur()
		if nameTok.Type != TokenIdent {
			p.errorAt(nameTok, "expected pipe name after '|'")
			p.skipTo(TokenPipe)
			continue
		}
		p.next()
		pipe := &Pipe{Name: strings.ToLower(nameTok.Value)}
		for p.cur().Type != TokenPipe && p.cur().Type != TokenEOF && !p.tooDeep {
			pipe.Args = append(pipe.Args, p.parseExpression())
			if p.accept(TokenComma) {
				continue
			}
			// Legacy Endgame field-list pipes accept space-separated fields
			// (e.g. `unique pid destination_port`), not just comma-separated.
			if fieldListPipes[pipe.Name] && isFieldStart(p.cur().Type) {
				continue
			}
			break
		}
		p.validatePipe(nameTok, pipe)
		pipes = append(pipes, pipe)
	}
	return pipes
}

// fieldListPipes are pipes whose arguments are a list of fields, which legacy
// EQL content may separate with spaces rather than commas.
var fieldListPipes = map[string]bool{
	"unique": true, "unique_count": true, "sort": true, "count": true,
}

// isFieldStart reports whether a token can begin a field reference.
func isFieldStart(t TokenType) bool {
	return t == TokenIdent || t == TokenBacktickIdent || t == TokenOptional
}

func (p *parser) validatePipe(nameTok Token, pipe *Pipe) {
	switch pipe.Name {
	case "head", "tail":
		if len(pipe.Args) != 1 {
			p.errorAt(nameTok, "pipe %q expects exactly one argument", pipe.Name)
			return
		}
		lit, ok := pipe.Args[0].(*Literal)
		if !ok || lit.Kind != LitNumber || strings.ContainsAny(lit.Text, ".eE") {
			p.errorAt(nameTok, "pipe %q expects an integer argument", pipe.Name)
		}
	case "count":
		// Endgame allowed `count field...`; zero or more args.
	case "unique", "unique_count", "sort":
		if len(pipe.Args) == 0 {
			p.errorAt(nameTok, "pipe %q expects at least one field", pipe.Name)
		}
	case "filter":
		if len(pipe.Args) != 1 {
			p.errorAt(nameTok, "pipe %q expects exactly one expression", pipe.Name)
		}
	default:
		p.errorAt(nameTok, "unknown pipe %q", pipe.Name)
	}
}

// skipTo advances until one of the given token types (or EOF) is current.
func (p *parser) skipTo(types ...TokenType) {
	for p.cur().Type != TokenEOF {
		for _, t := range types {
			if p.cur().Type == t {
				return
			}
		}
		p.next()
	}
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

func (p *parser) enter() bool {
	p.depth++
	if p.depth > maxExprDepth {
		if !p.tooDeep {
			p.tooDeep = true
			p.errs = append(p.errs, "expression nesting too deep")
		}
		return false
	}
	return true
}

func (p *parser) leave() { p.depth-- }

func (p *parser) parseExpression() Expr {
	if !p.enter() {
		p.depth--
		return &BadExpr{}
	}
	defer p.leave()
	return p.parseOr()
}

func (p *parser) parseOr() Expr {
	left := p.parseAnd()
	for p.cur().Type == TokenOr && !p.tooDeep {
		p.next()
		right := p.parseAnd()
		left = &Binary{Op: "or", L: left, R: right}
	}
	return left
}

func (p *parser) parseAnd() Expr {
	left := p.parseNot()
	for p.cur().Type == TokenAnd && !p.tooDeep {
		p.next()
		right := p.parseNot()
		left = &Binary{Op: "and", L: left, R: right}
	}
	return left
}

func (p *parser) parseNot() Expr {
	// `not in` at this position belongs to a predicate, not a logical not;
	// that case is handled inside parsePredicated, so only a `not` NOT
	// followed by `in`/`in~` is a logical negation here.
	if p.cur().Type == TokenNot && p.peekType(1) != TokenIn && p.peekType(1) != TokenInTilde {
		if !p.enter() {
			p.depth--
			return &BadExpr{}
		}
		defer p.leave()
		p.next()
		return &Not{X: p.parseNot()}
	}
	return p.parsePredicated()
}

func (p *parser) parsePredicated() Expr {
	v := p.parseComparison()

	switch p.cur().Type {
	case TokenNot:
		if p.peekType(1) == TokenIn || p.peekType(1) == TokenInTilde {
			p.next()
			insensitive := p.cur().Type == TokenInTilde
			p.next()
			list := p.parseParenList()
			return &InExpr{X: v, List: list, Negated: true, Insensitive: insensitive}
		}
	case TokenIn, TokenInTilde:
		insensitive := p.cur().Type == TokenInTilde
		p.next()
		list := p.parseParenList()
		return &InExpr{X: v, List: list, Insensitive: insensitive}
	case TokenColon:
		p.next()
		return p.parsePatternRHS(PatternSeq, false, v)
	case TokenLike, TokenLikeTilde:
		insensitive := p.cur().Type == TokenLikeTilde
		p.next()
		return p.parsePatternRHS(PatternLike, insensitive, v)
	case TokenRegex, TokenRegexTilde:
		insensitive := p.cur().Type == TokenRegexTilde
		p.next()
		return p.parsePatternRHS(PatternRegex, insensitive, v)
	}
	return v
}

func (p *parser) parsePatternRHS(kind PatternKind, insensitive bool, x Expr) Expr {
	pe := &PatternExpr{Kind: kind, X: x, Insensitive: insensitive}
	if p.cur().Type == TokenLParen {
		p.next()
		pe.Parenthesized = true
		for p.cur().Type != TokenRParen && p.cur().Type != TokenEOF && !p.tooDeep {
			pe.Patterns = append(pe.Patterns, p.parsePatternConstant(kind))
			if !p.accept(TokenComma) {
				break
			}
		}
		if _, ok := p.expect(TokenRParen); !ok {
			p.skipTo(TokenRParen, TokenPipe, TokenAnd, TokenOr, TokenRBrack)
			p.accept(TokenRParen)
		}
		if len(pe.Patterns) == 0 {
			p.errs = append(p.errs, fmt.Sprintf("empty pattern list for %q", string(kind)))
		}
		return pe
	}
	pe.Patterns = []Expr{p.parsePatternConstant(kind)}
	return pe
}

// parsePatternConstant parses the RHS of :, like, regex — a constant per the
// grammar. Non-constants are parsed anyway (tolerance) with an error note.
func (p *parser) parsePatternConstant(kind PatternKind) Expr {
	t := p.cur()
	switch t.Type {
	case TokenString, TokenNumber, TokenTrue, TokenFalse, TokenNull:
		return p.parsePrimary()
	default:
		e := p.parseComparison()
		p.errorAt(t, "%q expects a literal value", string(kind))
		return e
	}
}

func (p *parser) parseParenList() []Expr {
	var list []Expr
	if _, ok := p.expect(TokenLParen); !ok {
		return list
	}
	for p.cur().Type != TokenRParen && p.cur().Type != TokenEOF && !p.tooDeep {
		list = append(list, p.parseExpression())
		if !p.accept(TokenComma) {
			break
		}
	}
	if _, ok := p.expect(TokenRParen); !ok {
		p.skipTo(TokenRParen, TokenPipe, TokenRBrack)
		p.accept(TokenRParen)
	}
	if len(list) == 0 {
		p.errs = append(p.errs, "empty list in 'in' expression")
	}
	return list
}

func (p *parser) parseComparison() Expr {
	left := p.parseAdditive()
	op := ""
	switch p.cur().Type {
	case TokenEQ:
		op = "=="
	case TokenNEQ:
		op = "!="
	case TokenLT:
		op = "<"
	case TokenLTE:
		op = "<="
	case TokenGT:
		op = ">"
	case TokenGTE:
		op = ">="
	case TokenAssign:
		// Legacy Endgame equality; modern EQL rejects bare `=`.
		op = "=="
	default:
		return left
	}
	p.next()
	right := p.parseAdditive()
	cmp := &Binary{Op: op, L: left, R: right}

	// Comparison chaining (a < b <= c) is invalid EQL; note it but keep
	// parsing so extraction sees both comparisons.
	switch p.cur().Type {
	case TokenEQ, TokenNEQ, TokenLT, TokenLTE, TokenGT, TokenGTE:
		p.errorAt(p.cur(), "comparison chaining is not supported; use 'and'")
		opTok := p.next()
		right2 := p.parseAdditive()
		return &Binary{Op: "and", L: cmp, R: &Binary{Op: opTok.Value, L: right, R: right2}}
	}
	return cmp
}

func (p *parser) parseAdditive() Expr {
	left := p.parseMultiplicative()
	for !p.tooDeep {
		var op string
		switch p.cur().Type {
		case TokenPlus:
			op = "+"
		case TokenMinus:
			op = "-"
		default:
			return left
		}
		p.next()
		right := p.parseMultiplicative()
		left = &Binary{Op: op, L: left, R: right}
	}
	return left
}

func (p *parser) parseMultiplicative() Expr {
	left := p.parseUnary()
	for !p.tooDeep {
		var op string
		switch p.cur().Type {
		case TokenStar:
			op = "*"
		case TokenSlash:
			op = "/"
		case TokenPercent:
			op = "%"
		default:
			return left
		}
		p.next()
		right := p.parseUnary()
		left = &Binary{Op: op, L: left, R: right}
	}
	return left
}

func (p *parser) parseUnary() Expr {
	switch p.cur().Type {
	case TokenMinus:
		if !p.enter() {
			p.depth--
			return &BadExpr{}
		}
		defer p.leave()
		p.next()
		return &Unary{Op: "-", X: p.parseUnary()}
	case TokenPlus:
		if !p.enter() {
			p.depth--
			return &BadExpr{}
		}
		defer p.leave()
		p.next()
		return &Unary{Op: "+", X: p.parseUnary()}
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() Expr {
	if p.tooDeep {
		return &BadExpr{}
	}
	t := p.cur()
	switch t.Type {
	case TokenLParen:
		if !p.enter() {
			p.depth--
			p.next()
			return &BadExpr{}
		}
		defer p.leave()
		p.next()
		inner := p.parseExpression()
		if _, ok := p.expect(TokenRParen); !ok {
			p.skipTo(TokenRParen, TokenPipe, TokenRBrack)
			p.accept(TokenRParen)
		}
		return &Paren{X: inner}

	case TokenString:
		p.next()
		return &Literal{Kind: LitString, Value: t.Value, Raw: t.Raw}

	case TokenNumber:
		p.next()
		return &Literal{Kind: LitNumber, Text: t.Text}

	case TokenTrue, TokenFalse:
		p.next()
		return &Literal{Kind: LitBool, Bool: t.Type == TokenTrue}

	case TokenNull:
		p.next()
		return &Literal{Kind: LitNull}

	case TokenOptional:
		p.next()
		if p.cur().Type != TokenIdent && p.cur().Type != TokenBacktickIdent {
			p.errorAt(p.cur(), "expected field name after '?'")
			return &BadExpr{Near: "?"}
		}
		return p.parseField(true)

	case TokenBacktickIdent:
		return p.parseField(false)

	case TokenIdent:
		// Legacy lineage predicates: child of [...], descendant of [...],
		// event of [...].
		lower := strings.ToLower(t.Value)
		if (lower == "child" || lower == "descendant" || lower == "event") && p.peekType(1) == TokenOf {
			return p.parseLineage(lower)
		}
		// Function call only when '(' directly follows the identifier.
		if p.peekType(1) == TokenLParen {
			return p.parseCall()
		}
		if t.Tilde {
			p.errorAt(t, "'~' is only valid on function names")
		}
		return p.parseField(false)

	case TokenAny:
		// `any` as a value only appears in malformed queries; recover as a
		// field named any.
		p.next()
		p.errorAt(t, "'any' is a reserved keyword")
		return &Field{Path: []PathSeg{{Name: "any"}}}

	default:
		p.errorAt(t, "unexpected %s in expression", describeToken(t))
		p.next()
		return &BadExpr{Near: t.Text}
	}
}

func (p *parser) parseLineage(kind string) Expr {
	if !p.enter() {
		p.depth--
		return &BadExpr{}
	}
	defer p.leave()
	p.next() // child/descendant/event
	p.next() // of
	if _, ok := p.expect(TokenLBrack); !ok {
		return &BadExpr{Near: kind + " of"}
	}
	sub := p.parseEventQuery(true)
	if _, ok := p.expect(TokenRBrack); !ok {
		p.skipTo(TokenRBrack, TokenPipe)
		p.accept(TokenRBrack)
	}
	return &Lineage{Kind: kind, Sub: sub}
}

func (p *parser) parseCall() Expr {
	if !p.enter() {
		p.depth--
		return &BadExpr{}
	}
	defer p.leave()
	nameTok := p.next() // identifier
	p.next()            // (
	call := &Call{Name: nameTok.Value, Insensitive: nameTok.Tilde}
	for p.cur().Type != TokenRParen && p.cur().Type != TokenEOF && !p.tooDeep {
		call.Args = append(call.Args, p.parseExpression())
		if !p.accept(TokenComma) {
			break
		}
	}
	if _, ok := p.expect(TokenRParen); !ok {
		p.skipTo(TokenRParen, TokenPipe, TokenRBrack)
		p.accept(TokenRParen)
	}
	return call
}

// parseField parses a dotted, optionally indexed field path. The current
// token must be an identifier or backtick identifier.
func (p *parser) parseField(optional bool) Expr {
	f := &Field{Optional: optional}
	first := p.next()
	if first.Type == TokenBacktickIdent {
		f.Path = append(f.Path, PathSeg{Name: first.Value})
	} else {
		f.Path = append(f.Path, PathSeg{Name: first.Text})
	}

	for {
		switch {
		case p.cur().Type == TokenDot:
			nt := p.peekType(1)
			if nt != TokenIdent && nt != TokenBacktickIdent && !p.toks[minInt(p.pos+1, len(p.toks)-1)].isKeyword() {
				p.errorAt(p.cur(), "expected field name after '.'")
				p.next()
				return f
			}
			p.next()
			seg := p.next()
			name := seg.Text
			if seg.Type == TokenBacktickIdent {
				name = seg.Value
			}
			f.Path = append(f.Path, PathSeg{Name: name})

		case p.cur().Type == TokenLBrack && p.peekType(1) == TokenNumber && p.peekType(2) == TokenRBrack:
			p.next()
			numTok := p.next()
			p.next()
			idx, err := strconv.Atoi(numTok.Text)
			if err != nil {
				p.errorAt(numTok, "array index must be an integer")
				idx = 0
			}
			f.Path = append(f.Path, PathSeg{Index: idx, IsIndex: true})

		default:
			return f
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
