package eql

import (
	"strconv"
	"strings"
)

// Query is a parsed EQL statement: one body (event query, sequence, join, or
// sample) followed by zero or more pipes.
type Query struct {
	Body  QueryBody
	Pipes []*Pipe
}

// QueryBody is implemented by *EventQuery and *Sequence.
type QueryBody interface {
	render(b *strings.Builder)
}

// EventQuery is `category where condition`, `any where condition`, or — as a
// tolerated extension for extraction of stored rule fragments — a bare
// boolean expression without a category (Bare=true).
type EventQuery struct {
	Category    string // literal category text ("" when Any or Bare)
	CategoryAny bool   // `any where ...`
	Bare        bool   // no `category where` prefix, just an expression
	Where       Expr
}

// SequenceKind discriminates the three correlation constructs that share the
// Sequence node shape.
type SequenceKind string

const (
	KindSequence SequenceKind = "sequence"
	KindJoin     SequenceKind = "join"
	KindSample   SequenceKind = "sample"
)

// Sequence is `sequence ...`, `join ...`, or `sample ...`.
type Sequence struct {
	Kind        SequenceKind
	By          []Expr // global join keys (sequence/join/sample `by`)
	MaxSpan     string // raw duration text, e.g. "30s" ("" when absent)
	WithByOrder bool   // header was written `with maxspan=... by ...` (parsed-then-tolerated order)
	Steps       []*SequenceStep
	Until       *SequenceStep
}

// SequenceStep is one bracketed term of a sequence/join/sample.
type SequenceStep struct {
	Missing bool // opened with ![ — matches the absence of the event
	Query   *EventQuery
	By      []Expr // per-step join keys
	Runs    int    // `with runs=N`, 0 when absent
	WithKey string // raw key of a `with key=value` modifier ("runs" normally)
}

// Pipe is `| name arg1, arg2`.
type Pipe struct {
	Name string
	Args []Expr
}

// Expr is the interface implemented by all expression nodes.
type Expr interface {
	render(b *strings.Builder)
}

// Binary covers logical (and/or), comparison (== != < <= > >= : treated
// separately), and arithmetic (+ - * / %) operators. Op is the canonical
// lowercase operator text.
type Binary struct {
	Op   string
	L, R Expr
}

// Not is prefix `not expr`.
type Not struct {
	X Expr
}

// Paren preserves explicit grouping from the source.
type Paren struct {
	X Expr
}

// Unary is prefix arithmetic minus/plus.
type Unary struct {
	Op string
	X  Expr
}

// InExpr is `x [not] in[~] (a, b, c)`.
type InExpr struct {
	X           Expr
	List        []Expr
	Negated     bool
	Insensitive bool
}

// PatternKind distinguishes the string-matching predicates.
type PatternKind string

const (
	PatternSeq   PatternKind = ":"     // case-insensitive equality with wildcards
	PatternLike  PatternKind = "like"  // wildcard match
	PatternRegex PatternKind = "regex" // regular expression match
)

// PatternExpr is `x : v`, `x like v`, `x regex v`, or their list forms
// `x like ("a*", "b*")`. Insensitive marks the ~ variants (":" is inherently
// case-insensitive and keeps Insensitive=false).
type PatternExpr struct {
	Kind          PatternKind
	X             Expr
	Patterns      []Expr
	Parenthesized bool // list form was written with parentheses
	Insensitive   bool
}

// Call is a function call. Insensitive marks the ~ suffix (endsWith~).
type Call struct {
	Name        string
	Insensitive bool
	Args        []Expr
}

// PathSeg is one segment of a dotted field path: a name or an array index.
type PathSeg struct {
	Name    string
	Index   int
	IsIndex bool
}

// Field is a (possibly optional, possibly indexed) field reference such as
// `?process.args[0]` or “ `host-name`.ip “.
type Field struct {
	Optional bool
	Path     []PathSeg
}

// Name returns the canonical dotted path without the optional marker,
// e.g. "process.args[0]".
func (f *Field) Name() string {
	var b strings.Builder
	for i, s := range f.Path {
		if s.IsIndex {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.Index))
			b.WriteByte(']')
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Name)
	}
	return b.String()
}

// LiteralKind discriminates literal values.
type LiteralKind int

const (
	LitString LiteralKind = iota
	LitNumber
	LitBool
	LitNull
)

// Literal is a constant value. For strings, Value holds the decoded text;
// for numbers, Text holds the original notation.
type Literal struct {
	Kind  LiteralKind
	Value string // decoded string value
	Text  string // raw number text
	Bool  bool
	Raw   bool // originated from a raw string form
}

// Lineage is the legacy Endgame relationship predicate:
// `child of [process where ...]`, `descendant of [...]`, `event of [...]`.
type Lineage struct {
	Kind string // "child", "descendant", "event"
	Sub  *EventQuery
}

// BadExpr is a placeholder emitted during error recovery.
type BadExpr struct {
	Near string
}

// ---------------------------------------------------------------------------
// Rendering. String() output is canonical EQL that re-parses to the same AST
// (idempotent under parse∘render), which the round-trip tests rely on.
// ---------------------------------------------------------------------------

// String renders the query as canonical single-line EQL.
func (q *Query) String() string {
	var b strings.Builder
	if q.Body != nil {
		q.Body.render(&b)
	}
	for _, p := range q.Pipes {
		b.WriteString(" | ")
		b.WriteString(p.Name)
		for i, a := range p.Args {
			if i == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(", ")
			}
			a.render(&b)
		}
	}
	return b.String()
}

func (e *EventQuery) render(b *strings.Builder) {
	if e.Bare {
		if e.Where != nil {
			e.Where.render(b)
		}
		return
	}
	if e.CategoryAny {
		b.WriteString("any")
	} else {
		renderCategory(b, e.Category)
	}
	b.WriteString(" where ")
	if e.Where != nil {
		e.Where.render(b)
	}
}

func renderCategory(b *strings.Builder, cat string) {
	if isPlainIdent(cat) && keywords[strings.ToLower(cat)] == 0 {
		b.WriteString(cat)
		return
	}
	b.WriteString(quoteString(cat))
}

func (s *Sequence) render(b *strings.Builder) {
	b.WriteString(string(s.Kind))
	if len(s.By) > 0 {
		b.WriteString(" by ")
		renderExprList(b, s.By)
	}
	if s.MaxSpan != "" {
		b.WriteString(" with maxspan=")
		b.WriteString(s.MaxSpan)
	}
	for _, st := range s.Steps {
		b.WriteByte(' ')
		st.render(b)
	}
	if s.Until != nil {
		b.WriteString(" until ")
		s.Until.render(b)
	}
}

func (s *SequenceStep) render(b *strings.Builder) {
	if s.Missing {
		b.WriteString("![")
	} else {
		b.WriteByte('[')
	}
	s.Query.render(b)
	b.WriteByte(']')
	if len(s.By) > 0 {
		b.WriteString(" by ")
		renderExprList(b, s.By)
	}
	if s.WithKey != "" {
		b.WriteString(" with ")
		b.WriteString(s.WithKey)
		b.WriteByte('=')
		b.WriteString(strconv.Itoa(s.Runs))
	}
}

func renderExprList(b *strings.Builder, list []Expr) {
	for i, e := range list {
		if i > 0 {
			b.WriteString(", ")
		}
		e.render(b)
	}
}

// precedence returns the binding strength of an expression node for
// parenthesization during rendering; higher binds tighter.
func precedence(e Expr) int {
	switch v := e.(type) {
	case *Binary:
		switch v.Op {
		case "or":
			return 1
		case "and":
			return 2
		case "==", "!=", "<", "<=", ">", ">=":
			return 5
		case "+", "-":
			return 6
		default: // * / %
			return 7
		}
	case *Not:
		return 3
	case *InExpr, *PatternExpr:
		return 4
	case *Unary:
		return 8
	default:
		return 9
	}
}

func renderChild(b *strings.Builder, child Expr, parentPrec int, rightSide bool) {
	p := precedence(child)
	need := p < parentPrec || (rightSide && p == parentPrec)
	if need {
		b.WriteByte('(')
	}
	child.render(b)
	if need {
		b.WriteByte(')')
	}
}

func (e *Binary) render(b *strings.Builder) {
	p := precedence(e)
	renderChild(b, e.L, p, false)
	b.WriteByte(' ')
	b.WriteString(e.Op)
	b.WriteByte(' ')
	renderChild(b, e.R, p, true)
}

func (e *Not) render(b *strings.Builder) {
	b.WriteString("not ")
	renderChild(b, e.X, precedence(e), false)
}

func (e *Paren) render(b *strings.Builder) {
	b.WriteByte('(')
	e.X.render(b)
	b.WriteByte(')')
}

func (e *Unary) render(b *strings.Builder) {
	b.WriteString(e.Op)
	renderChild(b, e.X, precedence(e), false)
}

func (e *InExpr) render(b *strings.Builder) {
	renderChild(b, e.X, 4, false)
	if e.Negated {
		b.WriteString(" not")
	}
	b.WriteString(" in")
	if e.Insensitive {
		b.WriteByte('~')
	}
	b.WriteString(" (")
	renderExprList(b, e.List)
	b.WriteByte(')')
}

func (e *PatternExpr) render(b *strings.Builder) {
	renderChild(b, e.X, 4, false)
	b.WriteByte(' ')
	b.WriteString(string(e.Kind))
	if e.Insensitive {
		b.WriteByte('~')
	}
	b.WriteByte(' ')
	if len(e.Patterns) == 1 && !e.Parenthesized {
		e.Patterns[0].render(b)
		return
	}
	b.WriteByte('(')
	renderExprList(b, e.Patterns)
	b.WriteByte(')')
}

func (e *Call) render(b *strings.Builder) {
	b.WriteString(e.Name)
	if e.Insensitive {
		b.WriteByte('~')
	}
	b.WriteByte('(')
	renderExprList(b, e.Args)
	b.WriteByte(')')
}

func (e *Field) render(b *strings.Builder) {
	if e.Optional {
		b.WriteByte('?')
	}
	for i, s := range e.Path {
		if s.IsIndex {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.Index))
			b.WriteByte(']')
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		if isPlainIdent(s.Name) {
			b.WriteString(s.Name)
		} else {
			b.WriteByte('`')
			b.WriteString(strings.ReplaceAll(s.Name, "`", "``"))
			b.WriteByte('`')
		}
	}
}

func (e *Literal) render(b *strings.Builder) {
	switch e.Kind {
	case LitString:
		b.WriteString(quoteString(e.Value))
	case LitNumber:
		b.WriteString(e.Text)
	case LitBool:
		if e.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case LitNull:
		b.WriteString("null")
	}
}

func (e *Lineage) render(b *strings.Builder) {
	b.WriteString(e.Kind)
	b.WriteString(" of [")
	e.Sub.render(b)
	b.WriteByte(']')
}

func (e *BadExpr) render(b *strings.Builder) {
	b.WriteString("true /* unparsed */")
}

// quoteString renders a decoded string value as a canonical double-quoted
// EQL string literal.
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u{` + strconv.FormatInt(int64(r), 16) + `}`)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// isPlainIdent reports whether s can appear unquoted as an identifier.
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ExprString renders any expression node as canonical EQL text.
func ExprString(e Expr) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	e.render(&b)
	return b.String()
}
