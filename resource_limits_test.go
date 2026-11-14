package eql

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBooleanExpressionAccumulatesWithoutCopyingPrefixes(t *testing.T) {
	const count = 32
	parts := make([]string, count)
	conditions := make([]Condition, count)
	for i := range parts {
		parts[i] = fmt.Sprintf("field%d == %d", i, i)
	}
	query, err := Parse("process where " + strings.Join(parts, " or "))
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(5, func() {
		expression := buildConditionExpression(query, conditions)
		if expression == nil || len(expression.Children) != count {
			t.Fatal("flat disjunction lost its condition references")
		}
		for i, child := range expression.Children {
			if child.Operator != BooleanCondition || child.ConditionIndex != i {
				t.Fatalf("condition %d was reordered: %#v", i, child)
			}
		}
	})
	t.Logf("flat expression allocations: %.0f", allocations)
	// Leaves need one allocation each. Allow as many again for the builder
	// and growing child slice, but not a new group/slice for every prefix.
	if allocations > 2*count {
		t.Fatalf("flat boolean expression allocated %.0f objects; want at most %d", allocations, 2*count)
	}
}

func TestExtractionLimitDoesNotAffectAnotherCall(t *testing.T) {
	budget := newParseBudget(context.Background())
	budget.conditions = 0
	failed := extractWithBudget(`process where name == "editor"`, budget)
	if len(failed.Errors) == 0 {
		t.Fatal("expected the local budget to reject a condition")
	}
	result := ExtractConditions(`process where name == "editor"`)
	if len(result.Errors) != 0 || len(result.Conditions) != 1 || result.Conditions[0].Value != "editor" {
		t.Fatalf("one query's resource failure affected another: %#v", result)
	}
}
