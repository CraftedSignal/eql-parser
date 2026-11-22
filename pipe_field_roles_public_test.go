package eql

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Native legacy pipe acceptance does not imply Elasticsearch execution support.
// These public helpers must classify field AST roles rather than rendered text.
func TestPipeFieldRolesPublicIdentity(t *testing.T) {
	type lookup struct {
		name       string
		inPipes    bool
		provenance FieldProvenance
	}
	cases := []struct {
		name     string
		query    string
		pipe     string
		fields   []string
		args     []string
		optional bool
		lookups  []lookup
	}{
		{
			name:  "embedded backtick",
			query: "process where true | unique `tick``name`",
			pipe:  "unique", fields: []string{"tick`name"}, args: []string{"`tick``name`"},
			lookups: []lookup{{"tick`name", true, ProvenanceMain}, {"tick", false, ProvenanceUnknown}},
		},
		{
			name:  "literal only decoy",
			query: `process where true | filter x == "decoy"`,
			pipe:  "filter", fields: []string{"x"}, args: []string{`x == "decoy"`},
			lookups: []lookup{{"x", true, ProvenanceMain}, {"decoy", false, ProvenanceUnknown}},
		},
		{
			name:  "escaped name prefix decoy",
			query: "process where true | unique `host-name`",
			pipe:  "unique", fields: []string{"host-name"}, args: []string{"`host-name`"},
			lookups: []lookup{{"host-name", true, ProvenanceMain}, {"host", false, ProvenanceUnknown}, {"name", false, ProvenanceUnknown}},
		},
		{
			name:  "plain whole paths",
			query: "process where true | unique process.name, pid",
			pipe:  "unique", fields: []string{"process.name", "pid"}, args: []string{"process.name", "pid"},
			lookups: []lookup{{"process.name", true, ProvenanceMain}, {"pid", true, ProvenanceMain}, {"process", false, ProvenanceUnknown}, {"name", false, ProvenanceUnknown}, {"id", false, ProvenanceUnknown}},
		},
		{
			name:  "optional plain path",
			query: "process where true | unique ?process.name",
			pipe:  "unique", fields: []string{"process.name"}, args: []string{"?process.name"}, optional: true,
			lookups: []lookup{{"process.name", true, ProvenanceMain}, {"name", false, ProvenanceUnknown}},
		},
		{
			name:  "optional escaped path",
			query: "process where true | unique ?`tick``name`",
			pipe:  "unique", fields: []string{"tick`name"}, args: []string{"?`tick``name`"}, optional: true,
			lookups: []lookup{{"tick`name", true, ProvenanceMain}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, err := Parse(tc.query)
			if err != nil || ast == nil || len(ast.Pipes) != 1 || ast.Pipes[0].Name != tc.pipe || len(ast.Pipes[0].Args) != len(tc.args) {
				t.Fatalf("public AST rejected input or changed pipe: ast=%#v err=%v", ast, err)
			}
			for i, arg := range ast.Pipes[0].Args {
				if got := ExprString(arg); got != tc.args[i] {
					t.Fatalf("canonical arg[%d]=%q want %q", i, got, tc.args[i])
				}
				if tc.pipe == "unique" {
					field, ok := arg.(*Field)
					if !ok || field.Name() != tc.fields[i] || field.Optional != tc.optional {
						t.Fatalf("native field identity/optional carrier mismatch: %#v", arg)
					}
				}
			}
			parsed, semantic := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
			if len(parsed.Errors) != 0 || len(semantic.Errors) != 0 || !reflect.DeepEqual(parsed.Fields, tc.fields) || !reflect.DeepEqual(semantic.Fields, tc.fields) {
				t.Fatalf("extraction/analysis fields or errors differ: parsed=%#v semantic=%#v", parsed, semantic)
			}
			wantPipes := []PipeInfo{{Name: tc.pipe, Args: tc.args}}
			if !reflect.DeepEqual(parsed.Pipes, wantPipes) || !reflect.DeepEqual(semantic.Pipes, wantPipes) {
				t.Fatalf("canonical pipe carriers differ: parsed=%#v semantic=%#v", parsed.Pipes, semantic.Pipes)
			}
			if tc.pipe == "filter" && (len(parsed.Conditions) != 1 || parsed.Conditions[0].Field != "x" || parsed.Conditions[0].PipeStage != 1 || parsed.Conditions[0].PredicateSyntax != tc.args[0]) {
				t.Fatalf("filter field/stage/atomic syntax changed: %#v", parsed.Conditions)
			}
			carriers, err := json.Marshal(struct {
				Parsed   *ParseResult
				Semantic *SemanticQuery
			}{parsed, semantic})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("query=%q carriers=%s", tc.query, carriers)
			for _, want := range tc.lookups {
				usage := ClassifyFieldUsage(parsed, want.name)
				provenance := ClassifyFieldProvenance(parsed, want.name)
				t.Logf("lookup=%q usage=%+v provenance=%s", want.name, usage, provenance)
				if usage.InPipes != want.inPipes {
					t.Errorf("ClassifyFieldUsage(%q).InPipes=%v want %v", want.name, usage.InPipes, want.inPipes)
				}
				if provenance != want.provenance {
					t.Errorf("ClassifyFieldProvenance(%q)=%s want %s", want.name, provenance, want.provenance)
				}
			}
		})
	}
}

func TestPipeFieldRolesAllCallersAndOperands(t *testing.T) {
	for _, pipe := range []string{"unique", "unique_count", "sort", "count"} {
		t.Run(pipe, func(t *testing.T) {
			r := ExtractConditions("process where true | " + pipe + " ?`tick``name`")
			if len(r.Errors) != 0 || !reflect.DeepEqual(r.Fields, []string{"tick`name"}) || len(r.Pipes) != 1 || !reflect.DeepEqual(r.Pipes[0].Args, []string{"?`tick``name`"}) {
				t.Fatalf("unexpected carriers: %#v", r)
			}
			assertPipeRole(t, r, "tick`name", true, ProvenanceMain)
			assertPipeRole(t, r, "tick", false, ProvenanceUnknown)
		})
	}
	for _, tc := range []struct {
		query  string
		fields []string
		absent string
	}{
		{`process where true | filter left == ?right`, []string{"left", "right"}, "lefty"},
		{"process where true | filter length(`tick``name`) > 1", []string{"tick`name"}, "length"},
		{`process where true | filter startsWith(x, "decoy")`, []string{"x"}, "decoy"},
		{`process where true | filter "decoy"`, nil, "decoy"},
		{`process where true | count`, nil, "count"},
	} {
		r := ExtractConditions(tc.query)
		if len(r.Errors) != 0 || !reflect.DeepEqual(r.Fields, tc.fields) {
			t.Fatalf("unexpected carriers for %q: %#v", tc.query, r)
		}
		for _, field := range tc.fields {
			assertPipeRole(t, r, field, true, ProvenanceMain)
		}
		assertPipeRole(t, r, tc.absent, false, ProvenanceUnknown)
	}
}

func TestPipeFieldRolesDescriptorMutation(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(*ParseResult)
	}{
		{"name", func(r *ParseResult) { r.Pipes[0].Name = "count" }},
		{"argument", func(r *ParseResult) { r.Pipes[0].Args[0] = "other" }},
		{"argument append", func(r *ParseResult) { r.Pipes[0].Args = append(r.Pipes[0].Args, "other") }},
		{"reorder", func(r *ParseResult) { r.Pipes[0], r.Pipes[1] = r.Pipes[1], r.Pipes[0] }},
		{"remove", func(r *ParseResult) { r.Pipes = nil }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			r := ExtractConditions(`process where true | unique actual | sort second`)
			if len(r.Errors) != 0 {
				t.Fatal(r.Errors)
			}
			mutate.apply(r)
			assertPipeRole(t, r, "actual", false, ProvenanceUnknown)
			assertPipeRole(t, r, "other", false, ProvenanceUnknown)
			if mutate.name == "reorder" || mutate.name == "remove" {
				assertPipeRole(t, r, "second", false, ProvenanceUnknown)
			} else {
				assertPipeRole(t, r, "second", true, ProvenanceMain)
			}
		})
	}
}

func TestPipeFieldRolesManualAndJSON(t *testing.T) {
	original := ExtractConditions(`process where true | unique actual`)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored ParseResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Pipes, original.Pipes) || !reflect.DeepEqual(restored.Fields, original.Fields) {
		t.Fatalf("public JSON carriers changed: %#v", restored)
	}
	assertPipeRole(t, original, "actual", true, ProvenanceMain)
	assertPipeRole(t, &restored, "actual", false, ProvenanceUnknown)
	manual := &ParseResult{Pipes: []PipeInfo{{"unique", []string{"actual"}}}, Fields: []string{"actual"},
		Conditions: []Condition{{Field: "lhs", Value: "rhs", ValueIsField: true, PipeStage: 1}}, JoinKeys: []string{"key"}}
	assertPipeRole(t, manual, "actual", false, ProvenanceUnknown)
	assertPipeRole(t, manual, "lhs", true, ProvenanceMain)
	assertPipeRole(t, manual, "rhs", true, ProvenanceMain)
	if got := ClassifyFieldProvenance(manual, "key"); got != ProvenanceJoinKey || !ClassifyFieldUsage(manual, "key").IsJoinKey {
		t.Fatalf("independent join evidence lost: %s", got)
	}
	assertPipeRole(t, &ParseResult{}, "actual", false, ProvenanceUnknown)
	if ClassifyFieldProvenance(nil, "actual") != ProvenanceUnknown {
		t.Fatal("nil provenance changed")
	}
}

func assertPipeRole(t *testing.T, r *ParseResult, field string, inPipes bool, provenance FieldProvenance) {
	t.Helper()
	if got := ClassifyFieldUsage(r, field).InPipes; got != inPipes {
		t.Errorf("field %q InPipes=%v want %v", field, got, inPipes)
	}
	if got := ClassifyFieldProvenance(r, field); got != provenance {
		t.Errorf("field %q provenance=%s want %s", field, got, provenance)
	}
}

func TestPipeFieldRolesBudgetAndCancellation(t *testing.T) {
	f := &Field{Path: []PathSeg{{Name: "host-name"}}}
	budget := newParseBudget(context.Background())
	budget.output = 182 // existing 128-byte entry + 6 * 9-byte decoded name
	budget.work = 11    // one segment check, 9 source bytes, one output check
	if got := pipeFieldNameWithBudget(f, budget); got != "host-name" || budget.output != 0 || budget.work != 0 {
		t.Fatalf("exact accounting: name=%q output=%d work=%d", got, budget.output, budget.work)
	}
	for _, resource := range []string{"output", "work"} {
		t.Run(resource+" one short", func(t *testing.T) {
			b := newParseBudget(context.Background())
			b.output, b.work = 182, 11
			if resource == "output" {
				b.output--
			} else {
				b.work--
			}
			assertCorrelationKeyAbort(t, ErrResourceLimit, func() { pipeFieldNameWithBudget(f, b) })
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertCorrelationKeyAbort(t, context.Canceled, func() { pipeFieldNameWithBudget(f, newParseBudget(ctx)) })
	b := newParseBudget(context.Background())
	b.output = 128
	r := extractWithBudget(`process where true | unique actual | sort second`, b)
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], ErrResourceLimit.Error()) || len(r.Conditions) != 0 || r.Expression != nil || len(r.Pipes) != 0 || len(r.pipeFields) != 0 {
		t.Fatalf("resource rejection exposed partial pipe roles: %#v", r)
	}
	for _, query := range []string{`process where true | count`, `process where true | filter "decoy"`} {
		r := ExtractConditions(query)
		if len(r.Errors) != 0 || len(r.pipeFields) != 1 || !r.pipeFields[0].known || len(r.pipeFields[0].fields) != 0 {
			t.Fatalf("field-free pipe lacks known-empty metadata: %#v", r)
		}
	}
}
