package eql

import (
	"reflect"
	"strings"
	"testing"
)

func TestPredicatePrecedenceAST(t *testing.T) {
	member := func(x Expr, list ...Expr) Expr { return &InExpr{X: x, List: list} }
	field := wave16PrecedenceField
	number := func(text string) Expr { return &Literal{Kind: LitNumber, Text: text} }
	text := func(value string) Expr { return &Literal{Kind: LitString, Value: value} }
	boolean := func(value bool) Expr { return &Literal{Kind: LitBool, Bool: value} }
	null := &Literal{Kind: LitNull}
	for _, tc := range []struct {
		source string
		want   Expr
	}{
		{`process.name in ("cmd.exe") == true`, &Binary{Op: "==", L: member(field("process.name"), text("cmd.exe")), R: boolean(true)}},
		{`true == process.name in ("cmd.exe")`, &Binary{Op: "==", L: boolean(true), R: member(field("process.name"), text("cmd.exe"))}},
		{`-a in (1, 2)`, &Unary{Op: "-", X: member(field("a"), number("1"), number("2"))}},
		{`a * b in (1) + c`, &Binary{Op: "+", L: &Binary{Op: "*", L: field("a"), R: member(field("b"), number("1"))}, R: field("c")}},
		{`a in (1) * b + c == d`, &Binary{Op: "==", L: &Binary{Op: "+", L: &Binary{Op: "*", L: member(field("a"), number("1")), R: field("b")}, R: field("c")}, R: field("d")}},
		{`not -a in (1) * b + c >= d and x : "z*" or y == null`, &Binary{Op: "or", L: &Binary{Op: "and", L: &Not{X: &Binary{Op: ">=", L: &Binary{Op: "+", L: &Binary{Op: "*", L: &Unary{Op: "-", X: member(field("a"), number("1"))}, R: field("b")}, R: field("c")}, R: field("d")}}, R: &PatternExpr{Kind: PatternSeq, X: field("x"), Patterns: []Expr{text("z*")}}}, R: &Binary{Op: "==", L: field("y"), R: null}}},
		{`-a like~ ("x*", "y*") + b regex "q" == false`, &Binary{Op: "==", L: &Binary{Op: "+", L: &Unary{Op: "-", X: &PatternExpr{Kind: PatternLike, X: field("a"), Patterns: []Expr{text("x*"), text("y*")}, Parenthesized: true, Insensitive: true}}, R: &PatternExpr{Kind: PatternRegex, X: field("b"), Patterns: []Expr{text("q")}}}, R: boolean(false)}},
		{`a not in~ (b, 1 + 2, null)`, &InExpr{X: field("a"), List: []Expr{field("b"), &Binary{Op: "+", L: number("1"), R: number("2")}, null}, Negated: true, Insensitive: true}},
		{`not (a in (1))`, &Not{X: &Paren{X: member(field("a"), number("1"))}}},
		{`(a + b) in (1, 2)`, member(&Paren{X: &Binary{Op: "+", L: field("a"), R: field("b")}}, number("1"), number("2"))},
		{`(true == a) in (false, true)`, member(&Paren{X: &Binary{Op: "==", L: boolean(true), R: field("a")}}, boolean(false), boolean(true))},
		{`a regex~ ("x", "y")`, &PatternExpr{Kind: PatternRegex, X: field("a"), Patterns: []Expr{text("x"), text("y")}, Parenthesized: true, Insensitive: true}},
		{`f(a in (1), b like "x") == null`, &Binary{Op: "==", L: &Call{Name: "f", Args: []Expr{member(field("a"), number("1")), &PatternExpr{Kind: PatternLike, X: field("b"), Patterns: []Expr{text("x")}}}}, R: null}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got, err := ParseExpression(tc.source)
			if err != nil {
				t.Fatalf("valid precedence expression rejected: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("operator tree = %#v, want %#v", wave16PrecedenceTree(got), wave16PrecedenceTree(tc.want))
			}
			wave16CheckExpressionRoundTrip(t, got)
			query, err := Parse("process where " + tc.source)
			if err != nil {
				t.Fatalf("public event parse rejected expression: %v", err)
			}
			event, ok := query.Body.(*EventQuery)
			if !ok || event.Category != "process" || !reflect.DeepEqual(event.Where, tc.want) {
				t.Fatalf("event AST changed precedence: %#v", query.Body)
			}
			again, err := Parse(query.String())
			if err != nil {
				t.Fatalf("canonical event rejected: %s: %v", query.String(), err)
			}
			reparsed, ok := again.Body.(*EventQuery)
			if !ok || reparsed.Category != event.Category || !reflect.DeepEqual(wave16PrecedenceTree(event.Where), wave16PrecedenceTree(reparsed.Where)) {
				t.Fatalf("event round trip changed operator tree: %s", query.String())
			}
		})
	}
}

func TestPredicatePrecedenceRendererGroupsCompoundInputs(t *testing.T) {
	field := wave16PrecedenceField
	for _, expression := range []Expr{
		&InExpr{X: &InExpr{X: field("a"), List: []Expr{&Literal{Kind: LitNumber, Text: "1"}}}, List: []Expr{&Literal{Kind: LitBool, Bool: true}}},
		&InExpr{X: &Binary{Op: "==", L: field("a"), R: field("b")}, List: []Expr{&Literal{Kind: LitBool, Bool: true}}},
		&PatternExpr{Kind: PatternLike, X: &Unary{Op: "-", X: field("a")}, Patterns: []Expr{&Literal{Kind: LitString, Value: "x"}}},
		&Binary{Op: "==", L: &InExpr{X: field("a"), List: []Expr{&Literal{Kind: LitNull}}}, R: &PatternExpr{Kind: PatternLike, X: field("b"), Patterns: []Expr{&Literal{Kind: LitString, Value: "x"}}}},
	} {
		wave16CheckExpressionRoundTrip(t, expression)
	}
}

func TestPredicatePrecedenceSinglePredicateBoundary(t *testing.T) {
	for _, source := range []string{`a in (1) in (2)`, `a like "x" like "y"`, `a like (b)`} {
		if _, err := ParseExpression(source); err == nil {
			t.Errorf("repeated predicate or nonconstant pattern accepted: %s", source)
		}
	}
}

func wave16PrecedenceField(name string) Expr {
	var path []PathSeg
	for _, part := range strings.Split(name, ".") {
		path = append(path, PathSeg{Name: part})
	}
	return &Field{Path: path}
}

func wave16CheckExpressionRoundTrip(t *testing.T, expression Expr) {
	t.Helper()
	canonical := ExprString(expression)
	again, err := ParseExpression(canonical)
	if err != nil {
		t.Fatalf("canonical expression rejected: %s: %v", canonical, err)
	}
	if !reflect.DeepEqual(wave16PrecedenceTree(expression), wave16PrecedenceTree(again)) {
		t.Errorf("canonical %q changed operator tree: %#v -> %#v", canonical, wave16PrecedenceTree(expression), wave16PrecedenceTree(again))
	}
}

// Renderer-inserted grouping may add Paren nodes. Compare the full operator
// tree and payloads after removing only those semantically neutral wrappers.
func wave16PrecedenceTree(expression Expr) any {
	children := func(expressions []Expr) []any {
		out := make([]any, len(expressions))
		for i, child := range expressions {
			out[i] = wave16PrecedenceTree(child)
		}
		return out
	}
	switch node := expression.(type) {
	case *Paren:
		return wave16PrecedenceTree(node.X)
	case *Binary:
		return []any{"binary", node.Op, wave16PrecedenceTree(node.L), wave16PrecedenceTree(node.R)}
	case *Unary:
		return []any{"unary", node.Op, wave16PrecedenceTree(node.X)}
	case *Not:
		return []any{"not", wave16PrecedenceTree(node.X)}
	case *InExpr:
		return []any{"membership", node.Negated, node.Insensitive, wave16PrecedenceTree(node.X), children(node.List)}
	case *PatternExpr:
		return []any{"pattern", node.Kind, node.Insensitive, node.Parenthesized, wave16PrecedenceTree(node.X), children(node.Patterns)}
	case *Call:
		return []any{"call", node.Name, node.Insensitive, children(node.Args)}
	case *Field:
		return []any{"field", node.Optional, node.Path}
	case *Literal:
		return []any{"literal", node.Kind, node.Value, node.Text, node.Bool, node.Raw}
	default:
		return expression
	}
}
