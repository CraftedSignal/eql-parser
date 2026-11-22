package eql

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEscapedIdentifierIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, query, field, syntax string
	}{
		{"en dash", "process where `host–name` == \"x\"", "host–name", "`host–name` == \"x\""},
		{"ASCII hyphen", "process where `host-name` == \"x\"", "host-name", "`host-name` == \"x\""},
		{"literal backslash n", "process where `a\\nb` == \"x\"", `a\nb`, "`a\\nb` == \"x\""},
		{"literal backslash r", "process where `a\\rb` == \"x\"", `a\rb`, "`a\\rb` == \"x\""},
		{"literal backslash t", "process where `a\\tb` == \"x\"", `a\tb`, "`a\\tb` == \"x\""},
		{"doubled backtick", "process where `tick``name–part` == \"x\"", "tick`name–part", "`tick``name–part` == \"x\""},
		{"double quote", "process where `quote\"name–part` == \"x\"", "quote\"name–part", "`quote\"name–part` == \"x\""},
		{"single quote", "process where `quote'name–part` == \"x\"", "quote'name–part", "`quote'name–part` == \"x\""},
		{"line comment marker", "process where `slash//name–part` == \"x\"", "slash//name–part", "`slash//name–part` == \"x\""},
		{"block comment markers", "process where `block/*name*/–part` == \"x\"", "block/*name*/–part", "`block/*name*/–part` == \"x\""},
		{"closing comment marker", "process where `ending*/–part` == \"x\"", "ending*/–part", "`ending*/–part` == \"x\""},
		{"adjacent escaped path segments", "process where `a–b`.`c–d` == \"x\"", "a–b.c–d", "`a–b`.`c–d` == \"x\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeQuery(tc.query); got != tc.query {
				t.Errorf("normalization changed escaped identifier bytes: got %q want %q", got, tc.query)
			}
			parsed, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			event, ok := parsed.Body.(*EventQuery)
			if !ok {
				t.Fatalf("not an event query: %#v", parsed.Body)
			}
			predicate, ok := event.Where.(*Binary)
			if !ok {
				t.Fatalf("not a comparison: %#v", event.Where)
			}
			field, ok := predicate.L.(*Field)
			if !ok || field.Name() != tc.field {
				t.Fatalf("direct Parse field = %#v, want %q", predicate.L, tc.field)
			}
			extracted, semantic := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
			for path, result := range map[string]struct {
				conditions []Condition
				errors     []string
			}{"extract": {extracted.Conditions, extracted.Errors}, "analyze": {semantic.Conditions, semantic.Errors}} {
				if len(result.errors) != 0 || len(result.conditions) != 1 {
					t.Fatalf("%s conditions=%+v errors=%v", path, result.conditions, result.errors)
				}
				c := result.conditions[0]
				if c.Field != tc.field || c.PredicateSyntax != tc.syntax || c.Value != "x" || c.Operator != "==" {
					t.Errorf("%s changed field/predicate identity: %+v, want field=%q syntax=%q", path, c, tc.field, tc.syntax)
				}
			}
			// These non-flat fields remain opaque scalar syntax, not an added evaluator.
			if extracted.Expression == nil || extracted.Expression.Operator != BooleanScalar || extracted.Expression.Scalar == nil || extracted.Expression.Scalar.Syntax != tc.syntax {
				t.Errorf("extraction lost exact scalar: %+v", extracted.Expression)
			}
			if semantic.Expression == nil || semantic.Expression.Operator != BooleanScalar || semantic.Expression.Scalar == nil || semantic.Expression.Scalar.Syntax != tc.syntax {
				t.Errorf("semantic model lost exact scalar: %+v", semantic.Expression)
			}
		})
	}
}

func TestNormalizeEscapedIdentifierCollisionWitness(t *testing.T) {
	event := map[string]string{"host–name": "x", "host-name": "y"}
	for _, tc := range []struct{ query, value string }{
		{"process where `host–name` == \"x\"", "x"},
		{"process where `host-name` == \"x\"", "y"},
	} {
		r := ExtractConditions(tc.query)
		if len(r.Errors) != 0 || len(r.Conditions) != 1 || event[r.Conditions[0].Field] != tc.value {
			t.Errorf("exact field lookup selected a different event value: query=%q conditions=%+v errors=%v", tc.query, r.Conditions, r.Errors)
		}
	}
}

func TestNormalizeEscapedIdentifierBoundaries(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"process where `a\\nb` == \"x\"\\nand other == \"y\"", "process where `a\\nb` == \"x\"\nand other == \"y\""},
		{"process where `a\\rb` == \"x\"\\rand other == \"y\"", "process where `a\\rb` == \"x\"\rand other == \"y\""},
		{"process where `a\\tb` == \"x\"\\tand other == \"y\"", "process where `a\\tb` == \"x\"\tand other == \"y\""},
		{"process where `tick``//\"/*–*/` == \"x\" and other == \"y–z\"", "process where `tick``//\"/*–*/` == \"x\" and other == \"y–z\""},
	} {
		if got := NormalizeQuery(tc.query); got != tc.want {
			t.Errorf("normalization crossed identifier boundary: got %q want %q", got, tc.want)
		}
		r := ExtractConditions(tc.query)
		if len(r.Errors) != 0 || len(r.Conditions) != 2 || r.Conditions[1].Field != "other" {
			t.Errorf("identifier swallowed later predicate: %+v", r)
		}
	}
}

func TestNormalizeEscapedCorrelationKey(t *testing.T) {
	const query = "sequence by ?`host–name` [process where true] [network where true]"
	if got := NormalizeQuery(query); got != query {
		t.Errorf("correlation key changed: %q", got)
	}
	parsed, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	sequence, ok := parsed.Body.(*Sequence)
	if !ok || len(sequence.By) != 1 {
		t.Fatalf("missing direct correlation key: %#v", parsed.Body)
	}
	field, ok := sequence.By[0].(*Field)
	if !ok || field.Name() != "host–name" || !field.Optional {
		t.Fatalf("direct key changed: %#v", sequence.By[0])
	}
	r, s := ExtractConditions(query), AnalyzeQuery(query)
	if len(r.Errors) != 0 || len(s.Errors) != 0 || r.Sequence == nil || s.Sequence == nil {
		t.Fatalf("parse=%+v semantic=%+v", r, s)
	}
	for path, keys := range map[string][]CorrelationKey{"extract": r.Sequence.ByKeys, "analyze": s.Sequence.ByKeys} {
		if len(keys) != 1 || keys[0].Syntax != "?`host–name`" || keys[0].Field == nil || keys[0].Field.Name != "host–name" || !keys[0].Field.Optional {
			t.Errorf("%s correlation key changed: %+v", path, keys)
		}
	}
}

func TestNormalizeEscapedIdentifierExistingControls(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`process where plain == "x"`, `process where plain == "x"`},
		{"process where plain == \"x–y`\\n\"", "process where plain == \"x–y`\\n\""},
		{"process where plain == ?\"x–y`\\n\"", "process where plain == ?\"x–y`\\n\""},
		{"process where plain == \"\"\"x–y`\\n\"\"\"", "process where plain == \"\"\"x–y`\\n\"\"\""},
		{"/* ` and \" */ process where plain == \"x–y\"", "/* ` and \" */ process where plain == \"x–y\""},
		{"// ` and \"\nprocess where plain == \"x–y\"", "// ` and \"\nprocess where plain == \"x–y\""},
		{"process where size > −1 and plain == “x”", "process where size > -1 and plain == \"x\""},
		{"\ufeff```eql\nprocess where plain == \"x\"\n```", "process where plain == \"x\""},
		{"\ufeff```eql\nprocess where plain == “x”\n```", "process where plain == \"x\""},
		{"\u200b```eql\nprocess where plain == \"x\"\n```", "process where plain == \"x\""},
		{"```eql\nprocess where plain == \"x\"\n```\u200b", "process where plain == \"x\""},
		{"```eql\nprocess where plain == “x”", "process where plain == \"x\""},
		{`process where plain == "x";`, `process where plain == "x"`},
	} {
		if got := NormalizeQuery(tc.query); got != tc.want {
			t.Errorf("existing normalization changed: got %q want %q", got, tc.want)
		}
	}
	const unclosed = "process where `host–name\\n"
	if got := NormalizeQuery(unclosed); got != unclosed {
		t.Errorf("unterminated identifier bytes changed: %q", got)
	}
	if _, err := Parse(unclosed); err == nil || !strings.Contains(err.Error(), "unterminated backtick identifier") {
		t.Errorf("missing existing malformed-identifier diagnostic: %v", err)
	}
	for path, errs := range map[string][]string{"extract": ExtractConditions(unclosed).Errors, "analyze": AnalyzeQuery(unclosed).Errors} {
		if !strings.Contains(strings.Join(errs, " "), "unterminated backtick identifier") {
			t.Errorf("%s fabricated successful malformed identifier: %v", path, errs)
		}
	}
}

func TestNormalizeEscapedIdentifierContextAndLimits(t *testing.T) {
	const query = "process where `host–name` == \"x\""
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseContext(ctx, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Parse: %v", err)
	}
	extracted := ExtractConditionsContext(ctx, query)
	if len(extracted.Conditions) != 0 || extracted.Expression != nil || !strings.Contains(strings.Join(extracted.Errors, " "), context.Canceled.Error()) {
		t.Errorf("canceled extraction returned partial data: %+v", extracted)
	}
	semantic := AnalyzeQueryContext(ctx, query)
	if len(semantic.Conditions) != 0 || semantic.Expression != nil || !strings.Contains(strings.Join(semantic.Errors, " "), context.Canceled.Error()) {
		t.Errorf("canceled semantic conversion returned partial data: %+v", semantic)
	}
	budget := newParseBudget(context.Background())
	budget.work = 8
	r := extractWithBudget(query, budget)
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], ErrResourceLimit.Error()) || len(r.Conditions) != 0 || r.Expression != nil {
		t.Fatalf("bounded escaped identifier returned partial data: %+v", r)
	}
}

func TestNormalizeEscapedIdentifierFenceOuterLines(t *testing.T) {
	for _, tc := range []struct{ name, query, want, field, syntax string }{
		{"opening", "\u200b\n```eql\nprocess where plain == “x”\n```", `process where plain == "x"`, "plain", `plain == "x"`},
		{"closing", "```eql\nprocess where plain == “x”\n```\n\u200b", `process where plain == "x"`, "plain", `plain == "x"`},
		{"paired", "\u200b\n```eql\nprocess where plain == “x”\n```\n\u200b", `process where plain == "x"`, "plain", `plain == "x"`},
		{"protected identifier and smart quotes", "\u200b\n```eql\nprocess where `host–name` == “x”\n```\n\u200b", "process where `host–name` == \"x\"", "host–name", "`host–name` == \"x\""},
		{"unfenced", "\u200b\nprocess where `host–name` == “x”\n\u200b", "process where `host–name` == \"x\"", "host–name", "`host–name` == \"x\""},
		{"unterminated fence", "\u200b\n```eql\nprocess where `host–name` == “x”", "process where `host–name` == \"x\"", "host–name", "`host–name` == \"x\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeQuery(tc.query); got != tc.want {
				t.Errorf("outer fence lines changed normalization: got %q want %q", got, tc.want)
			}
			r, s := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
			for path, result := range map[string]struct {
				conditions []Condition
				errors     []string
			}{"extract": {r.Conditions, r.Errors}, "analyze": {s.Conditions, s.Errors}} {
				if len(result.errors) != 0 || len(result.conditions) != 1 || result.conditions[0].Field != tc.field || result.conditions[0].Value != "x" || result.conditions[0].PredicateSyntax != tc.syntax {
					t.Errorf("%s changed fence-contained predicate: conditions=%+v errors=%v", path, result.conditions, result.errors)
				}
			}
		})
	}
}
