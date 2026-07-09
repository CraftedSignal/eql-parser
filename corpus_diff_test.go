package eql

import (
	"fmt"
	"strings"
)

// diffConditions compares expected conditions against extracted ones and
// returns a human-readable difference ("" when they match on the compared
// fields).
func diffConditions(want []ExpectedCondition, got []Condition) string {
	if len(want) != len(got) {
		return fmt.Sprintf("count: want %d, got %d\n  want: %s\n  got:  %s",
			len(want), len(got), fmtExpected(want), fmtGot(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Field != g.Field {
			return fmt.Sprintf("condition %d field: want %q, got %q", i, w.Field, g.Field)
		}
		if w.Operator != g.Operator {
			return fmt.Sprintf("condition %d operator (field %s): want %q, got %q", i, w.Field, w.Operator, g.Operator)
		}
		if w.Value != g.Value {
			return fmt.Sprintf("condition %d value (field %s): want %q, got %q", i, w.Field, w.Value, g.Value)
		}
		if w.Negated != g.Negated {
			return fmt.Sprintf("condition %d negated (field %s): want %v, got %v", i, w.Field, w.Negated, g.Negated)
		}
		if w.SequenceStep != g.SequenceStep {
			return fmt.Sprintf("condition %d step (field %s): want %d, got %d", i, w.Field, w.SequenceStep, g.SequenceStep)
		}
		if strings.Join(w.Alternatives, "|") != strings.Join(g.Alternatives, "|") {
			return fmt.Sprintf("condition %d alternatives (field %s): want %v, got %v", i, w.Field, w.Alternatives, g.Alternatives)
		}
	}
	return ""
}

func fmtExpected(cs []ExpectedCondition) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s%s%s", c.Field, c.Operator, c.Value))
	}
	return strings.Join(parts, ", ")
}

func fmtGot(cs []Condition) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s%s%s", c.Field, c.Operator, c.Value))
	}
	return strings.Join(parts, ", ")
}
