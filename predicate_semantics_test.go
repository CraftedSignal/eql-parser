package eql

import (
	"fmt"
	"testing"
)

func TestSiblingOrMergeTruthTables(t *testing.T) {
	for _, tc := range []struct {
		text  string
		count int
		shape string
		want  func(bool, bool, bool) bool
	}{
		{`(a == "yes" and x == "u") or x == "v"`, 3, "or(and(0,1),2)", func(a, u, v bool) bool { return a && u || v }},
		{`x == "u" or (x == "v" and a == "yes")`, 3, "or(0,and(1,2))", func(a, u, v bool) bool { return u || v && a }},
		{`a == "yes" and (x == "u" or x == "v")`, 2, "and(0,1)", func(a, u, v bool) bool { return a && (u || v) }},
		{`(x == "u" or x == "v")`, 1, "0", func(a, u, v bool) bool { return u || v }},
		{`not (x == "u" or x == "v")`, 2, "and(0,1)", func(a, u, v bool) bool { return !(u || v) }},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r := ExtractConditions("any where " + tc.text)
			requireNoErrors(t, r)
			if len(r.Conditions) != tc.count {
				t.Fatalf("conditions: got %d want %d: %#v", len(r.Conditions), tc.count, r.Conditions)
			}
			if got := predicateShape(r.Expression); got != tc.shape {
				t.Fatalf("tree: got %s want %s", got, tc.shape)
			}
			for _, a := range []string{"yes", "no"} {
				for _, x := range []string{"u", "v", "other"} {
					got := evalEqualityTree(t, r.Expression, r.Conditions, map[string]string{"a": a, "x": x})
					if want := tc.want(a == "yes", x == "u", x == "v"); got != want {
						t.Errorf("a=%s x=%s got %v want %v", a, x, got, want)
					}
				}
			}
		})
	}
}
func predicateShape(e *ConditionExpression) string {
	if e == nil {
		return "nil"
	}
	if e.Operator == BooleanCondition {
		return fmt.Sprint(e.ConditionIndex)
	}
	s := string(e.Operator) + "("
	for i, c := range e.Children {
		if i > 0 {
			s += ","
		}
		s += predicateShape(c)
	}
	return s + ")"
}
func evalEqualityTree(t *testing.T, e *ConditionExpression, cs []Condition, row map[string]string) bool {
	t.Helper()
	if e == nil {
		t.Fatal("missing expression")
	}
	if e.Operator == BooleanCondition {
		c := cs[e.ConditionIndex]
		v := row[c.Field] == c.Value
		for _, alt := range c.Alternatives {
			v = v || row[c.Field] == alt
		}
		return v != c.Negated
	}
	v := e.Operator == BooleanAnd
	for _, c := range e.Children {
		b := evalEqualityTree(t, c, cs, row)
		if e.Operator == BooleanAnd {
			v = v && b
		} else {
			v = v || b
		}
	}
	return v
}
func TestSiblingOrMergeBarriers(t *testing.T) {
	for _, q := range []string{`(x == "u" and true) or x == "v"`, `?x == "u" or x == "v"`, `length(x) == 1 or length(x) == 2`, `not x == "u" or not x == "v"`} {
		r := ExtractConditions("any where " + q)
		requireNoErrors(t, r)
		if len(r.Conditions) != 2 {
			t.Errorf("%s merged: %#v", q, r.Conditions)
		}
	}
}
