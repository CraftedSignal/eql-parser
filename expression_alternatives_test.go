package eql

import "testing"

func TestExpressionPreservesAlternativesInGroupedLogic(t *testing.T) {
	result := ExtractConditions(`process where ((CommandLine : ("*whoami*", "*hostname*") and Image : "*cmd.exe") or ParentImage : "*winword.exe")`)
	if result.Expression == nil {
		t.Fatalf("expected expression, got nil conditions %#v errors %v", result.Conditions, result.Errors)
	}
	if len(result.Conditions) != 3 {
		t.Fatalf("expected compacted alternatives plus two scalar conditions, got %#v", result.Conditions)
	}
	if len(result.Conditions[0].Alternatives) != 2 {
		t.Fatalf("expected CommandLine alternatives to stay compacted, got %#v", result.Conditions[0])
	}
	if result.Expression.Operator != BooleanOr {
		t.Fatalf("expected grouped expression root OR, got %#v", result.Expression)
	}
	if len(result.Expression.Children) != 2 {
		t.Fatalf("expected two OR children, got %#v", result.Expression)
	}
	left := result.Expression.Children[0]
	if left.Operator != BooleanAnd || len(left.Children) != 2 {
		t.Fatalf("expected left OR branch to be AND(command alternatives, image), got %#v", left)
	}
	requireConditionIndex(t, left.Children[0], 0)
	requireConditionIndex(t, left.Children[1], 1)
	requireConditionIndex(t, result.Expression.Children[1], 2)
}

func requireConditionIndex(t *testing.T, expression *ConditionExpression, index int) {
	t.Helper()
	if expression == nil || expression.Operator != BooleanCondition || expression.ConditionIndex != index {
		t.Fatalf("expected condition index %d, got %#v", index, expression)
	}
}
