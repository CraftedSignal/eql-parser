package eql

import "testing"

// Elasticsearch's grammar lets an identifier start with '@'
// (IDENTIFIER : (LETTER | '_' | '@') (LETTER | DIGIT | '_')*), so @timestamp
// needs no backticks.
func TestAtSignIdentifierIsAField(t *testing.T) {
	res := ExtractConditions(`process where process.name == "a" and @timestamp > "2024-01-01" and user.name == "b"`)
	requireNoErrors(t, res)
	requireConditions(t, res, "process.name == a", "@timestamp > 2024-01-01", "user.name == b")
	if got := predicateShape(res.Expression); got != "and(0,1,2)" {
		t.Errorf("expression = %s, want and(0,1,2)", got)
	}
}

func TestAtSignIdentifierInSequenceStep(t *testing.T) {
	res := ExtractConditions(`sequence [process where @timestamp > "2024-01-01" and a == 1] [network where b == 2]`)
	requireNoErrors(t, res)
	requireConditions(t, res, "@timestamp > 2024-01-01", "a == 1", "b == 2")
	step := res.Sequence.Steps[0].Subquery
	requireConditions(t, step, "@timestamp > 2024-01-01", "a == 1")
}

func TestAtSignIdentifierRendersBare(t *testing.T) {
	for _, query := range []string{
		`process where @timestamp >= "2024-01-01" and host.@name == "x"`,
		"process where `@timestamp` >= \"2024-01-01\" and host.`@name` == \"x\"",
	} {
		q, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse(%s): %v", query, err)
		}
		const want = `process where @timestamp >= "2024-01-01" and host.@name == "x"`
		if got := q.String(); got != want {
			t.Errorf("Parse(%s).String() = %s, want %s", query, got, want)
		}
	}
}

// A field name that ends in another field's name is not that field.
func TestAtSignIdentifierWholePathInPipes(t *testing.T) {
	res := ExtractConditions(`process where a == 1 | unique @timestamp`)
	requireNoErrors(t, res)
	if got := ClassifyFieldProvenance(res, "timestamp"); got != ProvenanceUnknown {
		t.Errorf("provenance of timestamp = %q, want %q", got, ProvenanceUnknown)
	}
	if got := ClassifyFieldProvenance(res, "@timestamp"); got != ProvenanceMain {
		t.Errorf("provenance of @timestamp = %q, want %q", got, ProvenanceMain)
	}
}
