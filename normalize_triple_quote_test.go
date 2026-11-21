package eql

import "testing"

// Like the lexer and Elasticsearch ('"""' (~[\r\n])*? '"""' '"'? '"'?), the
// normalizer folds up to two quotes after the closing """ into the string, so
// it stays in step with the lexer and leaves later literals as written.
func TestNormalizeTripleQuotedExtraQuotes(t *testing.T) {
	for _, tc := range []struct{ query, a string }{
		{`process where a == """x"""" and b == "x–y"`, `x"`},
		{`process where a == """x""""" and b == "x–y"`, `x""`},
	} {
		if got := NormalizeQuery(tc.query); got != tc.query {
			t.Errorf("NormalizeQuery changed a string literal:\n in:  %s\n out: %s", tc.query, got)
		}
		res := ExtractConditions(tc.query)
		requireNoErrors(t, res)
		requireConditions(t, res, "a == "+tc.a, "b == x–y")
	}
	escaped := `process where a == """x"""" and b == "y\tz"`
	if got := NormalizeQuery(escaped); got != escaped {
		t.Errorf("NormalizeQuery decoded an escape inside a string literal:\n in:  %s\n out: %s", escaped, got)
	}
}
