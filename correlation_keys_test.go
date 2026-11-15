package eql_test

import (
	"encoding/json"
	"reflect"
	"testing"

	eql "github.com/craftedsignal/eql-parser"
)

func correlationJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestCorrelationKeysOriginalOptionalityJSON(t *testing.T) {
	cases := []struct {
		name, query           string
		global, first, second any
	}{
		{"global required", "sequence by process.pid [process where true] [network where true]", []any{map[string]any{"syntax": "process.pid", "field": map[string]any{"name": "process.pid"}}}, nil, nil},
		{"global optional", "sequence by ?process.pid [process where true] [network where true]", []any{map[string]any{"syntax": "?process.pid", "field": map[string]any{"name": "process.pid", "optional": true}}}, nil, nil},
		{"local required", "sequence [file where true] by file.path [process where true] by process.executable", nil, []any{map[string]any{"syntax": "file.path", "field": map[string]any{"name": "file.path"}}}, []any{map[string]any{"syntax": "process.executable", "field": map[string]any{"name": "process.executable"}}}},
		{"local optional", "sequence [file where true] by ?file.path [process where true] by ?process.executable", nil, []any{map[string]any{"syntax": "?file.path", "field": map[string]any{"name": "file.path", "optional": true}}}, []any{map[string]any{"syntax": "?process.executable", "field": map[string]any{"name": "process.executable", "optional": true}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parse := eql.ExtractConditions(tc.query)
			semantic := eql.AnalyzeQuery(tc.query)
			if len(parse.Errors) != 0 || len(semantic.Errors) != 0 {
				t.Fatalf("parse errors %v; semantic errors %v", parse.Errors, semantic.Errors)
			}
			for label, value := range map[string]any{"parse": parse, "semantic": semantic} {
				sequence, ok := correlationJSON(t, value)["sequence"].(map[string]any)
				if !ok {
					t.Fatalf("%s missing sequence", label)
				}
				if !reflect.DeepEqual(sequence["by_keys"], tc.global) {
					t.Errorf("%s global by_keys: got %#v, want %#v", label, sequence["by_keys"], tc.global)
				}
				steps := sequence["steps"].([]any)
				for i, want := range []any{tc.first, tc.second} {
					got := steps[i].(map[string]any)["by_keys"]
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s step %d by_keys: got %#v, want %#v", label, i, got, want)
					}
				}
			}
		})
	}
}

func TestCorrelationKeyFixtureASTAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name, query                  string
		global, first, second, until int
	}{
		{"until and duplicates", "sequence by ?host.id, host.id [process where true] by process.pid [network where true] by network.pid until [process where true] by ?process.pid", 2, 1, 1, 1},
		{"quoted", "sequence by ?`host-name`.id [process where true] [network where true]", 1, 0, 0, 0},
		{"indexed", "sequence by ?process.args[0] [process where true] [network where true]", 1, 0, 0, 0},
		{"computed", "sequence by length(process.name) [process where true] [network where true]", 1, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, err := eql.Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			sequence, ok := query.Body.(*eql.Sequence)
			if !ok || len(sequence.By) != tc.global || len(sequence.Steps) != 2 || len(sequence.Steps[0].By) != tc.first || len(sequence.Steps[1].By) != tc.second {
				t.Fatalf("unexpected AST: %#v", query.Body)
			}
			if tc.until == 0 && sequence.Until != nil || tc.until > 0 && (sequence.Until == nil || len(sequence.Until.By) != tc.until) {
				t.Fatalf("unexpected until AST: %#v", sequence.Until)
			}
		})
	}
}

func TestCorrelationKeysAcceptedScopesAndOpaqueJSON(t *testing.T) {
	for _, tc := range []struct {
		name, query                  string
		global, first, second, until any
	}{
		{"until and duplicates", "sequence by ?host.id, host.id [process where true] by process.pid [network where true] by network.pid until [process where true] by ?process.pid",
			[]any{map[string]any{"syntax": "?host.id", "field": map[string]any{"name": "host.id", "optional": true}}, map[string]any{"syntax": "host.id", "field": map[string]any{"name": "host.id"}}},
			[]any{map[string]any{"syntax": "process.pid", "field": map[string]any{"name": "process.pid"}}},
			[]any{map[string]any{"syntax": "network.pid", "field": map[string]any{"name": "network.pid"}}},
			[]any{map[string]any{"syntax": "?process.pid", "field": map[string]any{"name": "process.pid", "optional": true}}}},
		{"quoted", "sequence by ?`host-name`.id [process where true] [network where true]", []any{map[string]any{"syntax": "?`host-name`.id", "field": map[string]any{"name": "host-name.id", "optional": true}}}, nil, nil, nil},
		{"indexed", "sequence by ?process.args[0] [process where true] [network where true]", []any{map[string]any{"syntax": "?process.args[0]", "field": map[string]any{"name": "process.args[0]", "optional": true}}}, nil, nil, nil},
		{"computed", "sequence by length(process.name) [process where true] [network where true]", []any{map[string]any{"syntax": "length(process.name)"}}, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for label, value := range map[string]any{"parse": eql.ExtractConditions(tc.query), "semantic": eql.AnalyzeQuery(tc.query)} {
				object := correlationJSON(t, value)
				if errors, ok := object["errors"]; ok && len(errors.([]any)) > 0 {
					t.Fatalf("%s errors: %v", label, errors)
				}
				sequence := object["sequence"].(map[string]any)
				if got := sequence["by_keys"]; !reflect.DeepEqual(got, tc.global) {
					t.Errorf("%s global: got %#v, want %#v", label, got, tc.global)
				}
				steps := sequence["steps"].([]any)
				for i, want := range []any{tc.first, tc.second} {
					if got := steps[i].(map[string]any)["by_keys"]; !reflect.DeepEqual(got, want) {
						t.Errorf("%s step %d: got %#v, want %#v", label, i, got, want)
					}
				}
				if tc.until != nil {
					if got := sequence["until"].(map[string]any)["by_keys"]; !reflect.DeepEqual(got, tc.until) {
						t.Errorf("%s until: got %#v, want %#v", label, got, tc.until)
					}
				}
			}
		})
	}
}

func TestCorrelationKeysPreserveLegacyDiscoveryAndEventPredicates(t *testing.T) {
	for _, pair := range [][2]string{
		{"sequence by process.pid [process where true] [network where true]", "sequence by ?process.pid [process where true] [network where true]"},
		{"sequence [file where true] by file.path [process where true] by process.executable", "sequence [file where true] by ?file.path [process where true] by ?process.executable"},
	} {
		required, optional := eql.ExtractConditions(pair[0]), eql.ExtractConditions(pair[1])
		requiredSemantic, optionalSemantic := eql.AnalyzeQuery(pair[0]), eql.AnalyzeQuery(pair[1])
		if len(required.Errors)+len(optional.Errors)+len(requiredSemantic.Errors)+len(optionalSemantic.Errors) != 0 {
			t.Fatalf("unexpected errors: %v %v %v %v", required.Errors, optional.Errors, requiredSemantic.Errors, optionalSemantic.Errors)
		}
		if !reflect.DeepEqual(required.JoinKeys, optional.JoinKeys) || !reflect.DeepEqual(required.Fields, optional.Fields) || !reflect.DeepEqual(requiredSemantic.JoinKeys, optionalSemantic.JoinKeys) || !reflect.DeepEqual(requiredSemantic.Fields, optionalSemantic.Fields) || !reflect.DeepEqual(required.Sequence.ByFields, optional.Sequence.ByFields) || !reflect.DeepEqual(requiredSemantic.Sequence.ByFields, optionalSemantic.Sequence.ByFields) {
			t.Fatalf("legacy discovery changed: required %#v; optional %#v", required, optional)
		}
		for i := range required.Sequence.Steps {
			if !reflect.DeepEqual(required.Sequence.Steps[i].ByFields, optional.Sequence.Steps[i].ByFields) || !reflect.DeepEqual(requiredSemantic.Sequence.Steps[i].ByFields, optionalSemantic.Sequence.Steps[i].ByFields) {
				t.Fatalf("legacy step %d fields changed", i)
			}
			for _, step := range []*eql.SequenceStepInfo{&required.Sequence.Steps[i], &optional.Sequence.Steps[i]} {
				if step.Subquery == nil || step.Subquery.Expression == nil || step.Subquery.Expression.Scalar == nil || step.Subquery.Expression.Scalar.Syntax != "true" {
					t.Fatalf("step %d predicate lost: %#v", i, step)
				}
			}
		}
	}
}

func TestCorrelationKeyRawConditionCountContract(t *testing.T) {
	const merged = `sequence by process.pid with maxspan=5s [process where (a == "x" or a == "y") and gate == "yes"] with runs=2 ![network where false] until [process where not (event.type == "stop" or endsWith(process.name,".exe"))]`
	const unmerged = `sequence by process.pid with maxspan=5s [process where a == "x" and gate == "yes"] with runs=2 ![network where false] until [process where not (event.type == "stop" or endsWith(process.name,".exe"))]`
	for _, tc := range []struct {
		query string
		raw   int
	}{
		{merged, 3}, {unmerged, 2},
	} {
		parse, semantic := eql.ExtractConditions(tc.query), eql.AnalyzeQuery(tc.query)
		if len(parse.Errors)+len(semantic.Errors) != 0 {
			t.Fatalf("unexpected errors: %v %v", parse.Errors, semantic.Errors)
		}
		if parse.Sequence == nil || semantic.Sequence == nil || parse.Sequence.MaxSpan != "5s" || parse.Sequence.MaxSpanMS != 5000 || semantic.Sequence.MaxSpanMS != 5000 || len(parse.Sequence.Steps) != 2 || len(semantic.Sequence.Steps) != 2 {
			t.Fatalf("sequence metadata lost: %#v %#v", parse.Sequence, semantic.Sequence)
		}
		first, firstSemantic := parse.Sequence.Steps[0], semantic.Sequence.Steps[0]
		if first.Runs != 2 || firstSemantic.Runs != 2 || first.ConditionCount != tc.raw || firstSemantic.ConditionCount != tc.raw || first.Subquery == nil || firstSemantic.Subquery == nil || len(first.Subquery.Conditions) != 2 || len(firstSemantic.Subquery.Conditions) != 2 || first.Subquery.Expression == nil || firstSemantic.Subquery.Expression == nil {
			t.Fatalf("raw/returned count or predicate changed: %#v %#v", first, firstSemantic)
		}
		rootFirst := 0
		for _, condition := range parse.Conditions {
			if condition.SequenceStep == 0 && !condition.FromUntil {
				rootFirst++
			}
		}
		if rootFirst != 2 || first.Subquery.Expression.Operator != eql.BooleanAnd || firstSemantic.Subquery.Expression.Operator != eql.BooleanAnd || len(first.Subquery.Expression.Children) != 2 || len(firstSemantic.Subquery.Expression.Children) != 2 || first.Subquery.Conditions[0].Field != "a" || first.Subquery.Conditions[1].Field != "gate" {
			t.Fatalf("first event predicate changed: %#v %#v", first.Subquery, firstSemantic.Subquery)
		}
		if tc.raw == 3 && !reflect.DeepEqual(first.Subquery.Conditions[0].Alternatives, []string{"x", "y"}) {
			t.Fatalf("merged alternatives lost: %#v", first.Subquery.Conditions[0])
		}
		second, secondSemantic := parse.Sequence.Steps[1], semantic.Sequence.Steps[1]
		if !second.Missing || !secondSemantic.Missing || second.Subquery == nil || second.Subquery.Expression == nil || second.Subquery.Expression.Scalar == nil || second.Subquery.Expression.Scalar.Syntax != "false" || secondSemantic.Subquery == nil || secondSemantic.Subquery.Expression == nil || secondSemantic.Subquery.Expression.Scalar == nil || secondSemantic.Subquery.Expression.Scalar.Syntax != "false" {
			t.Fatalf("missing step changed: %#v %#v", second, secondSemantic)
		}
		if parse.Sequence.Until == nil || semantic.Sequence.Until == nil || parse.Sequence.Until.Subquery == nil || parse.Sequence.Until.Subquery.Expression == nil || semantic.Sequence.Until.Subquery == nil || semantic.Sequence.Until.Subquery.Expression == nil {
			t.Fatalf("until changed: %#v %#v", parse.Sequence.Until, semantic.Sequence.Until)
		}
		until := parse.Sequence.Until.Subquery
		if until.Expression.Operator != eql.BooleanAnd || len(until.Expression.Children) != 2 || len(until.Conditions) != 2 || !until.Conditions[0].Negated || !until.Conditions[1].Negated || until.Conditions[0].Field != "event.type" || until.Conditions[1].Field != "process.name" || until.Conditions[1].Function != "endsWith" {
			t.Fatalf("until predicate changed: %#v", until)
		}
	}
}
