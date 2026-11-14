package eql

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestKeywordFieldCanonicalAST(t *testing.T) {
	for _, word := range []string{"true", "false", "null", "and", "or", "not", "in", "like", "regex", "where", "sequence", "join", "sample", "until", "by", "with", "maxspan", "of", "any", "TRUE"} {
		t.Run(word, func(t *testing.T) {
			source := "endsWith(`" + word + "`, \"x\")"
			original, err := ParseExpression(source)
			if err != nil {
				t.Fatal(err)
			}
			canonical := ExprString(original)
			reparsed, err := ParseExpression(canonical)
			if err != nil {
				t.Fatalf("%q: %v", canonical, err)
			}
			if !reflect.DeepEqual(original, reparsed) {
				t.Fatalf("AST changed: %q => %q: %#v / %#v", source, canonical, original, reparsed)
			}
			r := ExtractConditions("any where " + source)
			requireNoErrors(t, r)
			if r.Expression == nil || r.Expression.Scalar == nil {
				t.Fatalf("missing scalar: %#v", r.Expression)
			}
			scalarAST, err := ParseExpression(r.Expression.Scalar.Syntax)
			if err != nil || !reflect.DeepEqual(original, scalarAST) {
				t.Fatalf("scalar changed AST: %q, %v", r.Expression.Scalar.Syntax, err)
			}
		})
	}
}

func TestAmbiguousFieldPathsRemainDistinct(t *testing.T) {
	query := "any where `x[0]` == \"a\" or x[0] == \"b\""
	r := ExtractConditions(query)
	requireNoErrors(t, r)
	if len(r.Conditions) != 2 || r.Conditions[0].RHS == nil || r.Conditions[0].RHS.Kind != OperandString || r.Conditions[1].RHS == nil || r.Conditions[1].RHS.Kind != OperandString {
		t.Fatalf("distinct conditions collapsed: %#v", r.Conditions)
	}
	if r.Expression == nil || r.Expression.Operator != BooleanOr || len(r.Expression.Children) != 2 {
		t.Fatalf("lost OR: %#v", r.Expression)
	}
	for i, child := range r.Expression.Children {
		if child.Operator != BooleanScalar || child.Scalar == nil {
			t.Fatalf("child %d is lossy: %#v", i, child)
		}
		original, err := ParseExpression(r.Conditions[i].PredicateSyntax)
		if err != nil {
			t.Fatal(err)
		}
		scalar, err := ParseExpression(child.Scalar.Syntax)
		if err != nil || !reflect.DeepEqual(original, scalar) {
			t.Fatalf("child %d scalar lost syntax: %v", i, err)
		}
	}
	s := AnalyzeQuery(query)
	if len(s.Errors) != 0 || len(s.Conditions) != 2 || s.Expression == nil || s.Expression.Operator != BooleanOr || s.Expression.Children[0].Operator != BooleanScalar || s.Expression.Children[1].Operator != BooleanScalar {
		t.Fatalf("semantic field paths collapsed: %#v", s)
	}
	for _, rhs := range []string{"`y[0]`", "y[0]", "`y.z`"} {
		r := ExtractConditions("any where x == " + rhs)
		requireNoErrors(t, r)
		if len(r.Conditions) != 1 || r.Conditions[0].RHS == nil || r.Conditions[0].RHS.Kind != OperandExpression || r.Conditions[0].RHS.Text != rhs || r.Expression == nil || r.Expression.Operator != BooleanScalar {
			t.Fatalf("ambiguous RHS %q: %#v", rhs, r)
		}
	}
	control := ExtractConditions(`any where x.y == "a" or x.y == "b"`)
	requireNoErrors(t, control)
	if len(control.Conditions) != 1 || control.Expression == nil || control.Expression.Operator != BooleanCondition {
		t.Fatalf("plain dotted control changed: %#v", control)
	}
}

func TestScalarPersistentSyntaxUsesOutputBudget(t *testing.T) {
	long := ""
	for i := 0; i < 120; i++ {
		long += "<"
	}
	expr, err := ParseExpression(`unknown("` + long + `")`)
	if err != nil {
		t.Fatal(err)
	}
	budget := newParseBudget(context.Background())
	budget.output = 700
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("escaped scalar syntax exceeded output budget without abort")
		}
		aborted, ok := value.(parseAbort)
		if !ok || !errors.Is(aborted.err, ErrResourceLimit) {
			t.Fatalf("unexpected abort: %#v", value)
		}
	}()
	(&eqlExpressionBuilder{budget: budget}).expr(expr, false)
}
