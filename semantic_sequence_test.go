package eql

import (
	"strconv"
	"strings"
	"testing"
)

// sequenceRootShape renders a semantic tree with each condition leaf's step.
func sequenceRootShape(e *SemanticExpression) string {
	if e == nil {
		return "nil"
	}
	switch e.Operator {
	case BooleanCondition:
		return condSig(*e.Condition) + "@" + strconv.Itoa(e.Condition.SequenceStep)
	case BooleanScalar:
		return "scalar(" + e.Scalar.Syntax + ")"
	}
	parts := make([]string, len(e.Children))
	for i, child := range e.Children {
		parts[i] = sequenceRootShape(child)
	}
	return string(e.Operator) + "(" + strings.Join(parts, ", ") + ")"
}

// A sequence's root expression ANDs its steps; each step keeps its own
// grouping, so an OR inside a step cannot reach another step's conditions.
func TestAnalyzeSequenceRootKeepsStepGrouping(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{
			`sequence [a where x == 1] [b where y == 2 or z == 3]`,
			`and(x == 1@0, or(y == 2@1, z == 3@1))`,
		},
		{
			`sequence by host.id [a where x == 1 or (y == 2 and z == 3)] [b where w == 4]`,
			`and(or(x == 1@0, and(y == 2@0, z == 3@0)), w == 4@1)`,
		},
		{
			`sequence [a where length(x) > 1] [b where y == 2]`,
			`and(scalar(length(x) > 1), y == 2@1)`,
		},
	} {
		sem := AnalyzeQuery(tc.query)
		if len(sem.Errors) != 0 {
			t.Fatalf("%s: errors %q", tc.query, sem.Errors)
		}
		if got := sequenceRootShape(sem.Expression); got != tc.want {
			t.Errorf("%s\n  root = %s\n  want   %s", tc.query, got, tc.want)
		}
	}
}

// A missing event and an until clause are not met by the matched events, so
// they stay out of the root expression; Sequence still describes them.
func TestAnalyzeSequenceRootLeavesOutMissingAndUntil(t *testing.T) {
	sem := AnalyzeQuery(`sequence with maxspan=1m [a where x == 1] ![b where y == 2] [c where z == 3] until [d where w == 4]`)
	if len(sem.Errors) != 0 {
		t.Fatalf("errors %q", sem.Errors)
	}
	if got, want := sequenceRootShape(sem.Expression), `and(x == 1@0, z == 3@2)`; got != want {
		t.Errorf("root = %s, want %s", got, want)
	}
	if !sem.Sequence.Steps[1].Missing || sem.Sequence.Steps[1].Subquery.Expression == nil || sem.Sequence.Until.Subquery.Expression == nil {
		t.Errorf("missing step or until lost from Sequence: %+v", sem.Sequence)
	}
}

// A stored result whose step carries no conditions adds nothing to the root.
func TestSemanticSequenceRootSkipsEmptyStep(t *testing.T) {
	sem := semanticFromExtraction(&ParseResult{Sequence: &SequenceInfo{Kind: "sequence", Steps: []SequenceStepInfo{
		{Subquery: &ParseResult{}},
		{Subquery: &ParseResult{Conditions: []Condition{{Field: "a", Operator: "==", Value: "1"}}}},
	}}})
	if got, want := sequenceRootShape(sem.Expression), `a == 1@1`; got != want {
		t.Errorf("root = %s, want %s", got, want)
	}
}
