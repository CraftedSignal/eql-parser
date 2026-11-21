package main

import (
	"strings"
	"testing"

	eql "github.com/craftedsignal/eql-parser"
	sigma "github.com/craftedsignal/sigma-parser"
)

func sigmaRuleText(detection string) string {
	return "title: t\nlogsource:\n  product: generic\ndetection:\n" + detection
}

// eqlShape renders an EQL boolean tree with field names as leaves; "!" marks
// a negated leaf.
func eqlShape(res *eql.ParseResult, e *eql.ConditionExpression) string {
	switch e.Operator {
	case eql.BooleanCondition:
		c := res.Conditions[e.ConditionIndex]
		if c.Negated {
			return "!" + c.Field
		}
		return c.Field
	case eql.BooleanScalar:
		return "scalar(" + e.Scalar.Syntax + ")"
	}
	parts := make([]string, len(e.Children))
	for i, child := range e.Children {
		parts[i] = eqlShape(res, child)
	}
	return string(e.Operator) + "(" + strings.Join(parts, ",") + ")"
}

// sigmaShape renders a Sigma boolean tree the same way.
func sigmaShape(e *sigma.Expression) string {
	switch e.Kind {
	case sigma.ExpressionCondition:
		if e.Condition.Negated {
			return "!" + e.Condition.Field
		}
		return e.Condition.Field
	case sigma.ExpressionNot:
		if e.Children[0].Kind == sigma.ExpressionCondition {
			return "!" + sigmaShape(e.Children[0])
		}
		return "not(" + sigmaShape(e.Children[0]) + ")"
	}
	parts := make([]string, len(e.Children))
	for i, child := range e.Children {
		parts[i] = sigmaShape(child)
	}
	return string(e.Kind) + "(" + strings.Join(parts, ",") + ")"
}

// Sigma -> EQL keeps the rule's grouping and negation.
func TestSigmaToEQLKeepsBooleanStructure(t *testing.T) {
	const selections = "  sel1:\n    a: x\n  sel2:\n    b: y\n  sel3:\n    c: z\n"
	for _, tc := range []struct{ condition, want string }{
		{"(sel1 or sel2) and sel3", "and(or(a,b),c)"},
		{"sel1 or sel2 and sel3", "or(a,and(b,c))"},
		{"not (sel1 and sel2)", "or(!a,!b)"},
		{"1 of sel* and not sel3", "and(or(a,b,c),!c)"},
	} {
		sig := sigma.ExtractConditions(sigmaRuleText(selections + "  condition: " + tc.condition + "\n"))
		query, class := SigmaToEQL(sig)
		if class != transField {
			t.Fatalf("%s: class %q, query %s", tc.condition, class, query)
		}
		res := eql.ExtractConditions(query)
		if len(res.Errors) != 0 {
			t.Fatalf("%s: %s: errors %q", tc.condition, query, res.Errors)
		}
		if got := eqlShape(res, res.Expression); got != tc.want {
			t.Errorf("%s: %s means %s, want %s", tc.condition, query, got, tc.want)
		}
	}
}

// "2 of" has no EQL form, so it is classified instead of written as an OR.
func TestSigmaToEQLThresholdIsNotTranslated(t *testing.T) {
	sig := sigma.ExtractConditions(sigmaRuleText("  sel1:\n    a: x\n  sel2:\n    b: y\n  sel3:\n    c: z\n  condition: 2 of sel*\n"))
	if query, class := SigmaToEQL(sig); class != transComplex {
		t.Errorf("2 of sel*: class %q, query %s; want %q", class, query, transComplex)
	}
}

// translateEQL runs EQL -> Sigma and parses the Sigma rule it writes.
func translateEQL(t *testing.T, query string) (*sigma.ParseResult, translateability, bool) {
	t.Helper()
	res := eql.ExtractConditions(query)
	if len(res.Errors) != 0 {
		t.Fatalf("%s: errors %q", query, res.Errors)
	}
	det, class, ok := EQLToSigmaDetection(res)
	if !ok {
		return nil, class, false
	}
	yml, err := marshalSigma("t", det)
	if err != nil {
		t.Fatalf("%s: marshal: %v", query, err)
	}
	sig := sigma.ExtractConditions(yml)
	if len(sig.Errors) != 0 {
		t.Fatalf("%s: Sigma rule rejected: %q\n%s", query, sig.Errors, yml)
	}
	return sig, class, true
}

// EQL -> Sigma keeps the query's and/or structure instead of ANDing every
// condition.
func TestEQLToSigmaKeepsBooleanStructure(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		class       translateability
	}{
		{`any where a == "x" or b == "y"`, "or(a,b)", transField},
		{`any where not (a == "x" and b == "y")`, "or(!a,!b)", transField},
		{`any where (a == "x" or b == "y") and c == "z"`, "and(or(a,b),c)", transField},
		{`any where cidrMatch(d, "10.0.0.0/8") or a == "x"`, "or(d,a)", transField},
		{`process where a == "x" or b == "y" | filter count > 25`, "or(a,b)", transAggregate},
	} {
		sig, class, ok := translateEQL(t, tc.query)
		if !ok || class != tc.class {
			t.Fatalf("%s: ok=%v class=%q, want class %q", tc.query, ok, class, tc.class)
		}
		if got := sigmaShape(sig.Expression); got != tc.want {
			t.Errorf("%s: Sigma rule means %s, want %s", tc.query, got, tc.want)
		}
	}
}

// Sigma's re is case-sensitive unless it carries the i sub-modifier, and takes
// no cased modifier.
func TestEQLToSigmaRegexKeepsCase(t *testing.T) {
	for query, caseSensitive := range map[string]bool{
		`any where a regex "x.*"`:  true,
		`any where a regex~ "x.*"`: false,
	} {
		sig, _, ok := translateEQL(t, query)
		if !ok || len(sig.Conditions) != 1 {
			t.Fatalf("%s: ok=%v conditions=%+v", query, ok, sig)
		}
		if c := sig.Conditions[0]; c.Operator != "matches" || c.CaseSensitive != caseSensitive {
			t.Errorf("%s: Sigma condition %+v, want matches with CaseSensitive=%v", query, c, caseSensitive)
		}
	}
}

// A function around the field, another field or a lineage changes what is
// compared; Sigma has no modifier for it, so the query is not translated. A
// predicate function such as startsWith, or a comparison on a quoted field,
// still is.
func TestEQLToSigmaRejectsFunctionWrappedFields(t *testing.T) {
	for _, query := range []string{
		`any where length(a) > 5`,
		`any where startsWith(a, "x") == false`,
		`any where a + b > 1`,
		`process where a == "x" and descendant of [process where b == "y"]`,
	} {
		if _, class, ok := translateEQL(t, query); ok {
			t.Errorf("%s: translated (class %q), want no translation", query, class)
		}
	}
	sig, _, ok := translateEQL(t, `any where startsWith(a, "x")`)
	if !ok || len(sig.Conditions) != 1 || sig.Conditions[0].Operator != "startswith" || !sig.Conditions[0].CaseSensitive {
		t.Errorf("startsWith(a, \"x\"): ok=%v Sigma %+v, want a|startswith|cased", ok, sig)
	}
	sig, _, ok = translateEQL(t, "any where `a/b` == \"x\" or c == \"y\"")
	if !ok || sigmaShape(sig.Expression) != "or(a/b,c)" {
		t.Errorf("`a/b` == \"x\" or c == \"y\": ok=%v Sigma %+v, want or(a/b,c)", ok, sig)
	}
}
