package eql

import (
	"strings"
	"testing"
	"time"
)

// This file drives the remaining defensive, recovery, and depth-limit branches
// to complete statement coverage. Several are white-box tests of unexported
// helpers or use the extractHook seam, because the corresponding paths cannot
// be reached with well-formed input.

// --- Panic recovery and timeout (via the extractHook seam) ---

func TestExtractPanicRecovery(t *testing.T) {
	extractHook = func(string) { panic("boom") }
	defer func() { extractHook = nil }()
	res := ExtractConditions(`process where a == 1`)
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "parser panic") {
		t.Fatalf("expected panic recovery, got %v", res.Errors)
	}
	if res.Conditions == nil {
		t.Error("Conditions must be non-nil after panic")
	}
}

func TestExtractTimeout(t *testing.T) {
	// A genuinely large input takes far longer than the tiny deadline to
	// parse, so the timeout branch fires deterministically. No hook is used,
	// so the abandoned parse goroutine touches only immutable package state
	// (race-free with the rest of the suite).
	old := MaxParseTime
	MaxParseTime = 1 * time.Millisecond
	defer func() { MaxParseTime = old }()

	var b strings.Builder
	b.WriteString("process where ")
	for i := 0; i < 40000; i++ {
		if i > 0 {
			b.WriteString(" and ")
		}
		b.WriteString(`a == "x"`)
	}
	res := ExtractConditions(b.String())
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "timeout") {
		t.Fatalf("expected timeout, got %v", res.Errors)
	}
}

// --- MaxInputSize guards ---

func TestParseInputTooLarge(t *testing.T) {
	huge := "process where a == " + strings.Repeat("1", MaxInputSize+1)
	if _, err := Parse(huge); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("Parse: %v", err)
	}
	if _, err := ParseExpression(huge); err == nil || !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("ParseExpression: %v", err)
	}
	res := ExtractConditions(huge)
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "maximum size") {
		t.Errorf("ExtractConditions: %v", res.Errors)
	}
}

// --- Depth-limit branches across the parser ---

// TestDeepNestingErrors confirms deeply nested input is rejected (not crashed)
// end to end. The exact per-function guard coverage is in TestDepthGuardsWhiteBox.
func TestDeepNestingErrors(t *testing.T) {
	rep := func(s string, k int) string { return strings.Repeat(s, k) }
	for _, q := range []string{
		`process where ` + rep("not ", 5000) + `a == 1`,
		`process where ` + rep("(", 5000) + "a == 1" + rep(")", 5000),
		`process where ` + rep("f(", 5000) + "x" + rep(")", 5000),
	} {
		res := ExtractConditions(q)
		if res == nil || res.Conditions == nil {
			t.Fatal("nil result for deep input")
		}
		if len(res.Errors) == 0 {
			t.Errorf("expected depth error for %q", truncate(q, 50))
		}
	}
}

// --- Specific value-parse error branches ---

func TestRunsNonIntegerValue(t *testing.T) {
	// `runs=1.5` lexes as a number token but fails integer conversion.
	res := ExtractConditions(`sequence [a where true] with runs=1.5 [b where true]`)
	if len(res.Errors) == 0 {
		t.Error("expected error for non-integer runs")
	}
}

func TestArrayIndexOverflow(t *testing.T) {
	// An out-of-range array index fails integer conversion but still parses.
	res := ExtractConditions(`process where process.args[99999999999999999999999] == "x"`)
	if res == nil || res.Conditions == nil {
		t.Fatal("nil result")
	}
	if len(res.Errors) == 0 {
		t.Error("expected array index error")
	}
}

func TestUntilWithoutBracket(t *testing.T) {
	// `until` not followed by '[' drives parseSequenceStep's default (nil) path.
	res := ExtractConditions(`sequence [a where true] [b where true] until x == 1`)
	if len(res.Errors) == 0 {
		t.Error("expected error for malformed until")
	}
}

// --- Lexer edge escapes ---

func TestBackspaceEscape(t *testing.T) {
	toks := lex(`"a\bc"`)
	if toks[0].Value != "a\bc" {
		t.Errorf("value = %q", toks[0].Value)
	}
}

func TestCarriageReturnEscape(t *testing.T) {
	toks := lex(`"a\rb"`)
	if toks[0].Value != "a\rb" {
		t.Errorf("value = %q", toks[0].Value)
	}
}

func TestNormalizeCodeFenceWithBlankLines(t *testing.T) {
	// Trailing blank lines after the closing fence exercise the empty-line
	// skip in the backward closing-fence scan.
	res := ExtractConditions("```eql\nprocess where a == 1\n```\n\n")
	requireNoErrors(t, res)
	requireConditions(t, res, `a == 1`)
}

// --- Renderer: right-side same-precedence parenthesization ---

func TestRenderChildRightSideParens(t *testing.T) {
	// a - (b - c): the right operand of '-' has equal precedence and must be
	// parenthesized. Built directly since the left-associative parser never
	// produces this shape without an explicit Paren node.
	fld := func(name string) Expr { return &Field{Path: []PathSeg{{Name: name}}} }
	e := &Binary{Op: "-", L: fld("a"), R: &Binary{Op: "-", L: fld("b"), R: fld("c")}}
	if got := ExprString(e); got != "a - (b - c)" {
		t.Errorf("render = %q, want %q", got, "a - (b - c)")
	}
	// And the left side at equal precedence is NOT parenthesized.
	e2 := &Binary{Op: "-", L: &Binary{Op: "-", L: fld("a"), R: fld("b")}, R: fld("c")}
	if got := ExprString(e2); got != "a - b - c" {
		t.Errorf("render = %q, want %q", got, "a - b - c")
	}
}

// --- DeduplicateConditions case-insensitive key branch ---

func TestDedupCaseInsensitiveKey(t *testing.T) {
	conds := []Condition{
		{Field: "f", Operator: ":", Value: "x", CaseInsensitive: true},
		{Field: "f", Operator: ":", Value: "x", CaseInsensitive: true}, // dup
		{Field: "f", Operator: ":", Value: "x", CaseInsensitive: false},
	}
	got := DeduplicateConditions(conds)
	if len(got) != 2 {
		t.Errorf("dedup = %d, want 2", len(got))
	}
}

// --- ClassifyFieldUsage pipe-stage branch ---

func TestClassifyFieldUsagePipeStage(t *testing.T) {
	res := ExtractConditions(`process where a == 1 | filter b : "x"`)
	requireNoErrors(t, res)
	u := ClassifyFieldUsage(res, "b")
	if !u.InPipes {
		t.Errorf("b should be InPipes: %+v", u)
	}
}

// --- White-box tests of unexported defensive guards ---

func TestNilGuardsWhiteBox(t *testing.T) {
	if extractSubquery(nil) == nil {
		t.Error("extractSubquery(nil) should return an empty result, not nil")
	}
	ex := newExtractor(&ParseResult{Conditions: []Condition{}})
	ex.extractQuery(nil)                            // q == nil guard
	ex.enterEventQuery(nil, 0, false, false, "")    // e == nil guard
	ex.extractSequence(nil)                         // s == nil guard
	if got := ex.registerJoinKey(nil); got != nil { // ExprString(nil) == "" guard
		t.Errorf("registerJoinKey(nil) = %v", got)
	}
	ex.emit(Condition{Field: "x"}) // Operator defaults to "=="
	if len(ex.res.Conditions) != 1 || ex.res.Conditions[0].Operator != "==" {
		t.Errorf("emit default operator: %+v", ex.res.Conditions)
	}
}

func TestLexerAdvanceAtEOF(t *testing.T) {
	l := &lexer{input: "", line: 1, col: 1}
	l.advance() // pos >= len guard: must be a no-op
	if l.pos != 0 {
		t.Errorf("advance at EOF moved pos to %d", l.pos)
	}
}

func TestPeekTypePastEnd(t *testing.T) {
	lx := newLexer("a")
	p := &parser{toks: lx.tokens}
	if got := p.peekType(9); got != TokenEOF {
		t.Errorf("peekType past end = %v, want EOF", got)
	}
}

func TestFieldInTextEmptyField(t *testing.T) {
	if fieldInText("some text", "") {
		t.Error("fieldInText with empty field should be false")
	}
}

// --- White-box depth guards ---

// TestDepthGuardsWhiteBox drives each depth guard deterministically by
// constructing a parser already at (or over) the ceiling. This is race-free
// (no shared global is mutated) and covers guards the recursive-descent loops
// short-circuit before reaching in normal parsing.
func TestDepthGuardsWhiteBox(t *testing.T) {
	// atCeiling builds a parser primed at the depth ceiling for the given input.
	atCeiling := func(src string) *parser {
		return &parser{toks: newLexer(src).tokens, depth: maxExprDepth}
	}
	tooDeep := func(src string) *parser {
		return &parser{toks: newLexer(src).tokens, tooDeep: true}
	}
	badExpr := func(name string, e Expr) {
		if _, ok := e.(*BadExpr); !ok {
			t.Errorf("%s: expected BadExpr, got %T", name, e)
		}
	}

	// enter() failures at the ceiling.
	badExpr("parseExpression", atCeiling("a").parseExpression())
	badExpr("parseNot", atCeiling("not a").parseNot())
	badExpr("parseUnary(-)", atCeiling("-a").parseUnary())
	badExpr("parseUnary(+)", atCeiling("+a").parseUnary())
	badExpr("parsePrimary(paren)", atCeiling("(a)").parsePrimary())
	badExpr("parseCall", atCeiling("f(x)").parsePrimary())
	badExpr("parseLineage", atCeiling("child of [process where a == 1]").parsePrimary())

	// tooDeep short-circuits.
	badExpr("parsePrimary(tooDeep)", tooDeep("a").parsePrimary())
	badExpr("parseExpression(tooDeep)", tooDeep("a").parseExpression())

	if got := tooDeep("by a, b").parseByKeys(); len(got) != 0 {
		t.Errorf("parseByKeys(tooDeep) = %v, want none", got)
	}
	if got := tooDeep("| head 1").parsePipes(); len(got) != 0 {
		t.Errorf("parsePipes(tooDeep) = %v, want none", got)
	}
	// parseSequence step-loop tooDeep guard.
	if s := tooDeep("sequence [a where true] [b where true]").parseSequence(KindSequence); len(s.Steps) != 0 {
		t.Errorf("parseSequence(tooDeep) steps = %d, want 0", len(s.Steps))
	}
	// parseParenList tooDeep guard (via an in-list).
	tooDeep("a in (b, c)").parsePredicated()
}
