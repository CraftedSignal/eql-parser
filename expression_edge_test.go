package eql

import "testing"

func TestExpressionBuilderEdgeBranches(t *testing.T) {
	builder := &eqlExpressionBuilder{conditions: []Condition{{Field: "process.name"}}}
	if got := builder.eventQuery(nil); got != nil {
		t.Fatalf("nil event query produced expression %#v", got)
	}
	if got := builder.eventQuery(&EventQuery{}); got != nil {
		t.Fatalf("event query without where produced expression %#v", got)
	}

	field := &Field{Path: []PathSeg{{Name: "process"}, {Name: "name"}}}
	literal := &Literal{Kind: LitString, Value: "cmd.exe"}
	if comparisonEmitsCondition(nil) {
		t.Fatal("nil comparison should not emit a condition")
	}
	if !comparisonEmitsCondition(&Binary{Op: "==", L: literal, R: field}) {
		t.Fatal("right-hand field comparison should emit a condition")
	}
	if comparisonEmitsCondition(&Binary{Op: "+", L: literal, R: literal}) {
		t.Fatal("non-field comparison should not emit a condition")
	}

	builder.next = len(builder.conditions)
	if got := builder.condition(); got != nil {
		t.Fatalf("exhausted condition builder produced expression %#v", got)
	}
}

func TestRemapConditionExpressionEdgeBranches(t *testing.T) {
	unknown := &ConditionExpression{Operator: BooleanOperator("xor")}
	if got := remapConditionExpression(unknown, []int{0}); got != nil {
		t.Fatalf("unknown operator remapped to %#v", got)
	}

	outOfRange := &ConditionExpression{Operator: BooleanCondition, ConditionIndex: 2}
	if got := remapConditionExpression(outOfRange, []int{0}); got != nil {
		t.Fatalf("out-of-range condition remapped to %#v", got)
	}

	dropped := &ConditionExpression{Operator: BooleanCondition, ConditionIndex: 0}
	if got := remapConditionExpression(dropped, []int{-1}); got != nil {
		t.Fatalf("dropped condition remapped to %#v", got)
	}
	allDropped := &ConditionExpression{Operator: BooleanAnd, Children: []*ConditionExpression{
		nil,
		{Operator: BooleanCondition, ConditionIndex: 0},
	}}
	if got := remapConditionExpression(allDropped, []int{-1}); got != nil {
		t.Fatalf("fully dropped expression remapped to %#v", got)
	}

	nested := &ConditionExpression{Operator: BooleanOr, Children: []*ConditionExpression{
		{Operator: BooleanCondition, ConditionIndex: 0},
		{Operator: BooleanOr, Children: []*ConditionExpression{
			{Operator: BooleanCondition, ConditionIndex: 1},
			{Operator: BooleanCondition, ConditionIndex: 2},
		}},
	}}
	got := remapConditionExpression(nested, []int{0, 1, 2})
	if got == nil || got.Operator != BooleanOr || len(got.Children) != 3 {
		t.Fatalf("expected flattened deduped OR, got %#v", got)
	}
}

func TestConditionExpressionEqualityAndOperatorSearchEdges(t *testing.T) {
	left := &ConditionExpression{Operator: BooleanAnd, Children: []*ConditionExpression{
		{Operator: BooleanCondition, ConditionIndex: 0},
	}}
	right := &ConditionExpression{Operator: BooleanAnd, Children: []*ConditionExpression{
		{Operator: BooleanCondition, ConditionIndex: 1},
	}}
	if equalConditionExpression(left, right) {
		t.Fatal("different child expressions should not compare equal")
	}
	if !equalConditionExpression(nil, nil) {
		t.Fatal("nil expressions should compare equal")
	}
	if expressionHasOperator(left, BooleanOr) {
		t.Fatal("AND-only expression should not report OR")
	}
	if expressionHasOperator(nil, BooleanOr) {
		t.Fatal("nil expression should not report an operator")
	}
}

func TestLexerFormFeedEscape(t *testing.T) {
	tokens := newLexer(`"a\fb"`).tokens
	if len(tokens) == 0 || tokens[0].Value != "a\fb" {
		t.Fatalf("expected form-feed escape, got %#v", tokens)
	}
}
