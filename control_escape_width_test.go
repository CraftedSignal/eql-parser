package eql

import "testing"

// Elastic requires 2–8 hex digits in Unicode escapes. Native reparse alone
// cannot prove that contract because the decoder also tolerates one digit.
func TestWave6aControlEscapeWidth(t *testing.T) {
	for _, tc := range []struct {
		name, query, predicate, value, field string
		raw, scalar                          bool
	}{
		{"low controls", `process where message == "\u{00}\u{08}\u{0f}"`, `message == "\u{00}\u{08}\u{0f}"`, "\x00\x08\x0f", "message", false, false},
		{"short escapes and escaped field", "process where `host-name` == \"\\b\\f\"", "`host-name` == \"\\u{08}\\u{0c}\"", "\b\f", "host-name", false, true},
		{"raw lookalike", `process where message == """\u{08}"""`, `message == "\\u{08}"`, `\u{08}`, "message", true, false},
		{"boundary and dedicated escapes", `process where message == "\u{10}\n\t\r"`, `message == "\u{10}\n\t\r"`, "\x10\n\t\r", "message", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeQuery(tc.query); got != tc.query {
				t.Errorf("normalization changed valid authored literal: %q", got)
			}
			q, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			event := q.Body.(*EventQuery)
			comparison := event.Where.(*Binary)
			literal := comparison.R.(*Literal)
			if literal.Kind != LitString || literal.Value != tc.value || literal.Raw != tc.raw || comparison.L.(*Field).Name() != tc.field {
				t.Errorf("decoded value/field/raw role changed: %+v / %+v", literal, comparison.L)
			}
			if got := q.String(); got != "process where "+tc.predicate {
				t.Errorf("canonical query has invalid escape width: got %q want %q", got, "process where "+tc.predicate)
			}
			if got := ExprString(event.Where); got != tc.predicate {
				t.Errorf("canonical expression: got %q want %q", got, tc.predicate)
			}
			r, s := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
			for api, result := range map[string]struct {
				conditions []Condition
				errors     []string
			}{"extract": {r.Conditions, r.Errors}, "analyze": {s.Conditions, s.Errors}} {
				if len(result.errors) != 0 || len(result.conditions) != 1 {
					t.Fatalf("%s invalid public model: %+v", api, result)
				}
				c := result.conditions[0]
				if c.Field != tc.field || c.Value != tc.value || c.RHS == nil || c.RHS.Kind != OperandString || c.RHS.Text != tc.value || c.PredicateSyntax != tc.predicate {
					t.Errorf("%s decoded operand/canonical predicate: %+v want syntax %q", api, c, tc.predicate)
				}
			}
			if r.Expression == nil || s.Expression == nil {
				t.Fatal("missing authoritative expression")
			}
			if tc.scalar {
				if r.Expression.Operator != BooleanScalar || s.Expression.Operator != BooleanScalar || r.Expression.Scalar == nil || s.Expression.Scalar == nil || r.Expression.Scalar.Syntax != tc.predicate || s.Expression.Scalar.Syntax != tc.predicate {
					t.Errorf("escaped field lost opaque canonical scalar: %+v / %+v", r.Expression, s.Expression)
				}
			} else if r.Expression.Operator == BooleanScalar || s.Expression.Operator == BooleanScalar {
				t.Error("ordinary string equality became opaque")
			}
		})
	}
	value := "\x00\b\f\x0f"
	if got := quoteString(value); got != `"\u{00}\u{08}\u{0c}\u{0f}"` {
		t.Errorf("direct low-control rendering: %q", got)
	}
	if rendered, bound := len(quoteString(value)), quotedStringBound(value, nil); rendered > bound {
		t.Errorf("existing conservative output bound exceeded: rendered=%d bound=%d", rendered, bound)
	}
}
