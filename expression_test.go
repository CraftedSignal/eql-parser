package eql

import "testing"

func TestExpressionPreservesNegatedPairwiseOr(t *testing.T) {
	result := ExtractConditions(`ProcessRollup2 where not (((process.command_line : "*whoami*" and image : "*cmd.exe") or (process.command_line : "*whoami*" and parent_image : "*winword.exe") or (image : "*cmd.exe" and parent_image : "*winword.exe")))`)
	requireNoErrors(t, result)

	if result.Expression == nil {
		t.Fatalf("expected expression, got nil with conditions %#v", result.Conditions)
	}
	if result.Expression.Operator != BooleanAnd {
		t.Fatalf("expected top-level AND after De Morgan, got %#v", result.Expression)
	}
	if len(result.Expression.Children) != 3 {
		t.Fatalf("expected three pair branches, got %#v", result.Expression)
	}
	for i, child := range result.Expression.Children {
		if child.Operator != BooleanOr {
			t.Fatalf("child %d should be OR branch, got %#v", i, child)
		}
		if len(child.Children) != 2 {
			t.Fatalf("child %d should have two leaves, got %#v", i, child)
		}
	}
	for i, condition := range result.Conditions {
		if !condition.Negated {
			t.Fatalf("condition %d should remain negated in flat output: %#v", i, result.Conditions)
		}
	}
}

func TestExpressionPreservesNegatedSameFieldOr(t *testing.T) {
	result := ExtractConditions(`process where not (process.command_line : "*whoami*") or not (process.command_line : "*/priv*")`)
	requireNoErrors(t, result)
	if result.Expression == nil {
		t.Fatalf("expected expression, got nil conditions %#v errors %v", result.Conditions, result.Errors)
	}
	if result.Expression.Operator != BooleanOr || len(result.Expression.Children) != 2 {
		t.Fatalf("expected OR over two negated leaves, got %#v", result.Expression)
	}
	if len(result.Conditions) != 2 {
		t.Fatalf("expected two separate negated conditions, got %#v", result.Conditions)
	}
	for _, condition := range result.Conditions {
		if !condition.Negated || len(condition.Alternatives) != 0 {
			t.Fatalf("negated OR leaves must not merge into alternatives: %#v", result.Conditions)
		}
	}
}
