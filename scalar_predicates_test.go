package eql

import (
	"encoding/json"
	"testing"
)

func TestCompletePredicateTrees(t *testing.T) {
	for _, tc := range []struct{ q, want string }{
		{`false`, `{"operator":"scalar","condition_index":0,"scalar":{"syntax":"false"}}`},
		{`true`, `{"operator":"scalar","condition_index":0,"scalar":{"syntax":"true"}}`},
		{`not false`, `{"operator":"scalar","condition_index":0,"scalar":{"syntax":"false","negated":true}}`},
		{`x == "a" and false`, `{"operator":"and","condition_index":0,"children":[{"operator":"condition","condition_index":0},{"operator":"scalar","condition_index":0,"scalar":{"syntax":"false"}}]}`},
		{`x == "a" or true`, `{"operator":"or","condition_index":0,"children":[{"operator":"condition","condition_index":0},{"operator":"scalar","condition_index":0,"scalar":{"syntax":"true"}}]}`},
		{`endsWith(x, "a") or x == "z"`, `{"operator":"or","condition_index":0,"children":[{"operator":"scalar","condition_index":0,"scalar":{"syntax":"endsWith(x, \"a\")"}},{"operator":"condition","condition_index":1}]}`},
		{`not (false or x == "a")`, `{"operator":"and","condition_index":0,"children":[{"operator":"scalar","condition_index":0,"scalar":{"syntax":"false","negated":true}},{"operator":"condition","condition_index":0}]}`},
		{`not not false`, `{"operator":"scalar","condition_index":0,"scalar":{"syntax":"false"}}`},
		{`1 == 2`, `{"operator":"scalar","condition_index":0,"scalar":{"syntax":"1 == 2"}}`},
	} {
		t.Run(tc.q, func(t *testing.T) {
			r := ExtractConditions("any where " + tc.q)
			requireNoErrors(t, r)
			data, err := json.Marshal(r.Expression)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("tree got %s want %s", data, tc.want)
			}
			s := AnalyzeQuery("any where " + tc.q)
			if len(s.Errors) != 0 || s.Expression == nil {
				t.Fatalf("semantic lost expression: %#v", s)
			}
		})
	}
}
func TestOpaquePredicatesRemainDistinct(t *testing.T) {
	for _, pair := range [][2]string{
		{`false`, `true`}, {`x == "a" and false`, `x == "a"`}, {`x == "a" or true`, `x == "a"`},
		{`endsWith(x,"abc")`, `endsWith("abc",x)`}, {`startsWith(x,"a") == true`, `startsWith(x,"b") == true`},
		{`between(x,"a","z") == "m"`, `between~(x,"b","z") == "m"`},
	} {
		a := AnalyzeQuery("any where " + pair[0])
		b := AnalyzeQuery("any where " + pair[1])
		if len(a.Errors)+len(b.Errors) != 0 {
			t.Fatalf("errors %#v %#v", a.Errors, b.Errors)
		}
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if string(aa) == string(bb) {
			t.Errorf("collapsed %q and %q: %s", pair[0], pair[1], aa)
		}
	}
}
func TestScalarCorrelationsAndPipeIndexes(t *testing.T) {
	s := AnalyzeQuery(`sequence [any where false] [any where true]`)
	if s.Sequence == nil || s.Sequence.Steps[0].Subquery.Expression == nil || s.Sequence.Steps[1].Subquery.Expression == nil {
		t.Fatalf("lost sequence scalar: %#v", s)
	}
	r := ExtractConditions(`any where unknown(x, "a") | filter false or y == "b"`)
	requireNoErrors(t, r)
	if len(r.Conditions) != 2 || r.Conditions[1].PipeStage != 1 {
		t.Fatalf("stage/index lost: %#v", r)
	}
	if got := predicateShape(r.Expression); got != "and(scalar(),or(scalar(),1))" {
		t.Fatalf("got %s", got)
	}
}

func TestUnknownBooleanNodesFailClosed(t *testing.T) {
	for _, fn := range []func(){
		func() { remapConditionExpression(&ConditionExpression{Operator: "xor"}, []int{0}) },
		func() { semanticExpressionFromParseExpression(&ConditionExpression{Operator: "xor"}, nil) },
	} {
		func() {
			defer func() {
				if _, ok := recover().(parseAbort); !ok {
					t.Error("unknown boolean node was silently dropped")
				}
			}()
			fn()
		}()
	}
}
