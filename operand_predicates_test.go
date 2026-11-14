package eql

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTypedPredicateOperands(t *testing.T) {
	for _, tc := range []struct {
		query string
		kind  OperandKind
		text  string
	}{
		{`?x == null`, OperandNull, ""},
		{`?x == "null"`, OperandString, "null"},
		{`x == true`, OperandBool, "true"},
		{`x == "true"`, OperandString, "true"},
		{`x == 123456789012345678901234567890e+42`, OperandNumber, "123456789012345678901234567890e+42"},
		{`x == ""`, OperandString, ""},
		{`x == ?y`, OperandField, "y"},
	} {
		r := ExtractConditions("any where " + tc.query)
		requireNoErrors(t, r)
		if len(r.Conditions) != 1 || r.Conditions[0].RHS == nil {
			t.Fatalf("%q: %#v", tc.query, r.Conditions)
		}
		c := r.Conditions[0]
		if c.RHS.Kind != tc.kind || c.RHS.Text != tc.text || c.PredicateSyntax != tc.query {
			t.Fatalf("%q: %#v", tc.query, c)
		}
		if tc.kind == OperandField && !c.RHS.Optional {
			t.Fatal("optional RHS field lost")
		}
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var restored Condition
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c, restored) {
			t.Fatalf("JSON roundtrip: %#v != %#v", c, restored)
		}
		s := AnalyzeQuery("any where " + tc.query)
		if len(s.Errors) != 0 || len(s.Conditions) != 1 || !reflect.DeepEqual(s.Conditions[0].RHS, c.RHS) {
			t.Fatalf("semantic %q: %#v", tc.query, s)
		}
	}
}

func TestTypedMembershipAndMerge(t *testing.T) {
	for _, tc := range []struct {
		query string
		kinds []OperandKind
		texts []string
	}{
		{`x in (1, "1", null, "", 1)`, []OperandKind{OperandNumber, OperandString, OperandNull, OperandString, OperandNumber}, []string{"1", "1", "", "", "1"}},
		{`x == null or x == "null"`, []OperandKind{OperandNull, OperandString}, []string{"", "null"}},
		{`x == 1 or x == "1"`, []OperandKind{OperandNumber, OperandString}, []string{"1", "1"}},
	} {
		r := ExtractConditions("any where " + tc.query)
		requireNoErrors(t, r)
		if len(r.Conditions) != 1 || r.Conditions[0].RHS == nil || r.Conditions[0].RHS.Kind != OperandList {
			t.Fatalf("%q: %#v", tc.query, r.Conditions)
		}
		c := r.Conditions[0]
		if len(c.RHS.Items) != len(tc.kinds) {
			t.Fatalf("%q: %#v", tc.query, c.RHS)
		}
		for i, item := range c.RHS.Items {
			if item.Kind != tc.kinds[i] || item.Text != tc.texts[i] {
				t.Fatalf("%q item %d: %#v", tc.query, i, item)
			}
		}
		if c.PredicateSyntax != tc.query {
			t.Fatalf("syntax %q: %q", tc.query, c.PredicateSyntax)
		}
	}
}

func TestPatternOperandShape(t *testing.T) {
	for _, tc := range []struct {
		query string
		kind  OperandKind
	}{
		{`x : "a"`, OperandString},
		{`x : ("a")`, OperandList},
	} {
		r := ExtractConditions("any where " + tc.query)
		requireNoErrors(t, r)
		if len(r.Conditions) != 1 || r.Conditions[0].RHS == nil || r.Conditions[0].RHS.Kind != tc.kind {
			t.Fatalf("%q: %#v", tc.query, r.Conditions)
		}
	}
}

func TestEmptyMembershipRHS(t *testing.T) {
	original := Condition{Field: "x", Operator: "in", RHS: &Operand{Kind: OperandList}}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Condition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RHS == nil || decoded.RHS.Kind != OperandList || len(decoded.RHS.Items) != 0 {
		t.Fatalf("empty list lost: %s %#v", encoded, decoded)
	}
}

func TestDedupPreservesPredicateMetadata(t *testing.T) {
	for _, pair := range [][2]string{
		{`x == "a"`, `?x == "a"`},
		{`x == 2`, `length(x) == 2`},
		{`x == y`, `x == "y"`},
		{`x == null`, `x == "null"`},
	} {
		a, b := ExtractConditions("any where "+pair[0]), ExtractConditions("any where "+pair[1])
		requireNoErrors(t, a)
		requireNoErrors(t, b)
		if got := DeduplicateConditions([]Condition{a.Conditions[0], b.Conditions[0]}); len(got) != 2 {
			t.Fatalf("collapsed %q / %q: %#v", pair[0], pair[1], got)
		}
	}
	base := Condition{Field: "a", Operator: "==", Alternatives: []string{"a\x1fb", ""}}
	other := base
	other.Alternatives = []string{"a", "b\x1f"}
	if got := DeduplicateConditions([]Condition{base, other}); len(got) != 2 {
		t.Fatalf("delimiter collision: %#v", got)
	}
	invalid := base
	invalid.Field = string([]byte{0xff})
	if got := DeduplicateConditions([]Condition{base, invalid}); len(got) != 2 {
		t.Fatalf("invalid UTF8 collision: %#v", got)
	}
	for _, changed := range []Condition{
		{Field: "a", Operator: "==", Alternatives: []string{"a\x1fb", ""}, FromMissing: true},
		{Field: "a", Operator: "==", Alternatives: []string{"a\x1fb", ""}, CoFields: []string{"z"}},
		{Field: "a", Operator: "==", Alternatives: []string{"a\x1fb", ""}, Function: "length"},
		{Field: "a", Operator: "==", Alternatives: []string{"a\x1fb", ""}, PredicateSyntax: `a == "a"`},
	} {
		if got := DeduplicateConditions([]Condition{base, changed}); len(got) != 2 {
			t.Fatalf("metadata collision: %#v", got)
		}
	}
}

func TestPredicateSyntaxCanonicalFixpoint(t *testing.T) {
	for _, query := range []string{
		`endsWith("abc", x)`, `endsWith(x, "abc")`, `between~(x, "a", "z") == "m"`,
		`x == null or x == "null"`, `x in (1, "1", 1)`, `3 < x`,
	} {
		r := ExtractConditions("any where " + query)
		requireNoErrors(t, r)
		for _, c := range r.Conditions {
			e, err := ParseExpression(c.PredicateSyntax)
			if err != nil {
				t.Fatalf("%q syntax %q: %v", query, c.PredicateSyntax, err)
			}
			if again := ExprString(e); again != c.PredicateSyntax {
				t.Fatalf("%q rendered %q then %q", query, c.PredicateSyntax, again)
			}
		}
	}
}

func TestPredicateMetadataOwnership(t *testing.T) {
	query := `any where x in ("a", "b")`
	r := ExtractConditions(query)
	s, other := AnalyzeQuery(query), AnalyzeQuery(query)
	requireNoErrors(t, r)
	if len(s.Errors)+len(other.Errors) != 0 {
		t.Fatal(s.Errors, other.Errors)
	}
	r.Conditions[0].RHS.Items[0].Text = "changed extraction"
	if s.Conditions[0].RHS.Items[0].Text != "a" {
		t.Fatal("extraction aliases semantic")
	}
	s.Conditions[0].RHS.Items[0].Text = "changed semantic"
	if s.Expression.Condition.RHS.Items[0].Text != "a" || other.Conditions[0].RHS.Items[0].Text != "a" {
		t.Fatal("semantic conditions alias tree or another result")
	}
	deduped := DeduplicateConditions(other.Conditions)
	deduped[0].RHS.Items[0].Text = "changed dedup"
	if other.Conditions[0].RHS.Items[0].Text != "a" {
		t.Fatal("dedup aliases input")
	}
	scalar := ExtractConditions(`any where false`)
	first, second := AnalyzeQuery(`any where false`), AnalyzeQuery(`any where false`)
	scalar.Expression.Scalar.Syntax = "changed"
	first.Expression.Scalar.Syntax = "changed"
	if second.Expression.Scalar.Syntax != "false" {
		t.Fatal("scalar aliases another result")
	}
}

func TestPredicateMetadataFailureOnlyBudget(t *testing.T) {
	budget := newParseBudget(context.Background())
	budget.output = 1
	r := extractWithBudget(`any where x in ("a", "b")`, budget)
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], ErrResourceLimit.Error()) || len(r.Conditions) != 0 || r.Expression != nil {
		t.Fatalf("partial resource failure: %#v", r)
	}
}

func TestLargeTypedPredicateSharesBudget(t *testing.T) {
	values := make([]string, 4000)
	for i := range values {
		values[i] = `"` + strings.Repeat("a", 56) + `"`
	}
	query := `any where x in (` + strings.Join(values, ", ") + `)`
	r := ExtractConditions(query)
	s := AnalyzeQuery(query)
	if len(r.Errors) != 0 || len(s.Errors) != 0 || len(r.Conditions) != 1 || len(s.Conditions) != 1 {
		t.Fatalf("large predicate failed: extraction=%v analysis=%v", r.Errors, s.Errors)
	}
	if len(r.Conditions[0].RHS.Items) != 4000 || len(s.Conditions[0].RHS.Items) != 4000 {
		t.Fatal("typed list truncated")
	}
}
