package eql

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const untilRunsBase = `sequence [process where process.name == "start"] [file where file.name == "out"] until [process where event.type == "stop"]`

func TestUntilRunsNormalizePublic(t *testing.T) {
	for _, runs := range []int{2, 100} {
		t.Run(fmt.Sprint(runs), func(t *testing.T) {
			query := untilRunsBase + fmt.Sprintf(" with runs=%d", runs)
			parsed, semantic := ExtractConditions(query), AnalyzeQuery(query)
			if len(parsed.Errors)+len(semantic.Errors) != 0 {
				t.Fatalf("errors: %v / %v", parsed.Errors, semantic.Errors)
			}
			if parsed.Sequence == nil || semantic.Sequence == nil || len(parsed.Sequence.Steps) != 3 || len(semantic.Sequence.Steps) != 3 {
				t.Fatalf("until repetition lost its additional positive term: parse=%+v semantic=%+v", parsed.Sequence, semantic.Sequence)
			}
			positive, final := parsed.Sequence.Steps[2], parsed.Sequence.Until
			sp, sf := semantic.Sequence.Steps[2], semantic.Sequence.Until
			if final == nil || sf == nil || positive.Runs != runs-1 || sp.Runs != runs-1 || final.Runs != 1 || sf.Runs != 1 || positive.Missing || sp.Missing || final.Missing || sf.Missing {
				t.Fatalf("normalized multiplicity/roles: %+v / %+v / %+v / %+v", positive, final, sp, sf)
			}
			for _, step := range []SequenceStepInfo{positive, *final} {
				if step.EventCategory != "process" || step.ConditionCount != 1 || step.Subquery == nil || len(step.Subquery.Conditions) != 1 || step.Subquery.Conditions[0].SequenceStep != 0 || step.Subquery.Conditions[0].FromUntil {
					t.Fatalf("self-contained extracted term lost: %+v", step)
				}
			}
			for _, conditions := range [][]Condition{parsed.Conditions, semantic.Conditions} {
				if len(conditions) != 4 {
					t.Fatalf("conditions=%+v; want two originals, positive copy, final until", conditions)
				}
				for i, field := range []string{"process.name", "file.name", "event.type", "event.type"} {
					c := conditions[i]
					if c.Field != field || c.SequenceStep != i || c.FromUntil != (i == 3) || c.FromMissing || c.RHS == nil || c.RHS.Kind != OperandString {
						t.Fatalf("condition %d roles/identity: %+v", i, c)
					}
				}
			}
			// Extraction's sequence root is intentionally not a single-event
			// Boolean tree; Analyze supplies a positive-term predicate projection.
			if parsed.Expression != nil {
				t.Fatal("extraction root unexpectedly became a sequence evaluator")
			}
			leaves := untilRunsSemanticLeaves(semantic.Expression)
			if len(leaves) != 3 {
				t.Fatalf("semantic positive tree omitted/duplicated a term: %+v", leaves)
			}
			for i, c := range leaves {
				if c.SequenceStep != i || c.FromUntil || c.FromMissing || c.Field != parsed.Conditions[i].Field {
					t.Fatalf("semantic leaf %d: %+v", i, c)
				}
			}
			for _, value := range []any{parsed, semantic} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), fmt.Sprintf(`"runs":%d`, runs-1)) || !strings.Contains(string(data), `"runs":1`) {
					t.Fatalf("JSON omitted normalized multiplicity: %s", data)
				}
			}
		})
	}
}

func untilRunsSemanticLeaves(root *SemanticExpression) []Condition {
	if root == nil {
		return nil
	}
	var out []Condition
	if root.Condition != nil {
		out = append(out, *root.Condition)
	}
	for _, child := range root.Children {
		out = append(out, untilRunsSemanticLeaves(child)...)
	}
	return out
}

func TestUntilRunsOneAndAbsentCompatibility(t *testing.T) {
	if !reflect.DeepEqual(ExtractConditions(untilRunsBase), ExtractConditions(untilRunsBase+" with runs=1")) || !reflect.DeepEqual(AnalyzeQuery(untilRunsBase), AnalyzeQuery(untilRunsBase+" with runs=1")) {
		t.Fatal("absent/explicit one changed complete public objects")
	}
	for _, query := range []string{untilRunsBase, untilRunsBase + " with runs=1"} {
		r := ExtractConditions(query)
		if len(r.Errors) != 0 || r.Sequence == nil || len(r.Sequence.Steps) != 2 || r.Sequence.Until == nil || r.Sequence.Until.Runs != 0 {
			t.Fatalf("legacy one/absent convention changed: %+v", r)
		}
	}
	ordinary := ExtractConditions(`sequence [process where process.name == "start"] with runs=2 [file where file.name == "out"]`)
	if len(ordinary.Errors) != 0 || ordinary.Sequence == nil || len(ordinary.Sequence.Steps) != 2 || ordinary.Sequence.Steps[0].Runs != 2 || ordinary.Sequence.Until != nil {
		t.Fatalf("ordinary positive repetition changed: %+v", ordinary)
	}
}

func TestUntilRunsKeysBooleanOwnershipAndAST(t *testing.T) {
	const query = `sequence by ?host.id with maxspan=5s [process where process.name == "start"] by process.entity_id [file where file.name == "out"] by file.path until [process where (?event.type == "stop" or user.name == "halt") and process.pid == 7] by ?process.entity_id with runs=2`
	ast, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(ast)
	if err != nil {
		t.Fatal(err)
	}
	text := ast.String()
	result := &ParseResult{Conditions: []Condition{}}
	newExtractor(result).extractQuery(ast)
	after, err := json.Marshal(ast)
	if err != nil || string(before) != string(after) || ast.String() != text {
		t.Fatalf("extraction mutated authored AST: %v", err)
	}
	authored := ast.Body.(*Sequence)
	if authored.Until.Runs != 2 || authored.Until.WithKey != "runs" || len(authored.Steps) != 2 {
		t.Fatalf("authored until/step placement changed: %+v", authored)
	}
	parsed := ExtractConditions(query)
	semantic := AnalyzeQuery(query)
	if len(parsed.Errors)+len(semantic.Errors) != 0 || parsed.Sequence == nil || semantic.Sequence == nil || len(parsed.Sequence.Steps) != 3 || len(semantic.Sequence.Steps) != 3 {
		t.Fatalf("keyed repetition normalization failed: %+v / %+v", parsed, semantic)
	}
	p, u := &parsed.Sequence.Steps[2], parsed.Sequence.Until
	if u == nil || p.Subquery == nil || u.Subquery == nil || p.Subquery == u.Subquery || p.Subquery.Expression == u.Subquery.Expression || !reflect.DeepEqual(p.Subquery, u.Subquery) {
		t.Fatal("positive/final until subqueries must be equal independent owners")
	}
	if parsed.Sequence.MaxSpanMS != 5000 || len(parsed.Sequence.ByKeys) != 1 || parsed.Sequence.ByKeys[0].Syntax != "?host.id" || parsed.Sequence.ByKeys[0].Field == nil || !parsed.Sequence.ByKeys[0].Field.Optional || len(p.ByKeys) != 1 || p.ByKeys[0].Syntax != "?process.entity_id" || p.ByKeys[0].Field == nil || !p.ByKeys[0].Field.Optional || !reflect.DeepEqual(p.ByKeys, u.ByKeys) || p.ByKeys[0].Field == u.ByKeys[0].Field {
		t.Fatal("global/local optional keys lost or aliased")
	}
	if p.ConditionCount != 3 || u.ConditionCount != 3 || len(p.Subquery.Conditions) != 3 || !p.Subquery.Conditions[0].IsOptional || p.Subquery.Conditions[2].RHS == nil || p.Subquery.Conditions[2].RHS.Kind != OperandNumber || p.Subquery.Conditions[2].RHS.Text != "7" {
		t.Fatalf("typed nested predicate lost: %+v", p.Subquery)
	}
	for _, sub := range []*ParseResult{p.Subquery, u.Subquery} {
		if sub.Expression == nil || sub.Expression.Operator != BooleanAnd || len(sub.Expression.Children) != 2 || sub.Expression.Children[0].Operator != BooleanOr || len(sub.Expression.Children[0].Children) != 2 {
			t.Fatalf("nested AND/OR grouping lost: %+v", sub.Expression)
		}
	}
	if len(parsed.Conditions) != 8 {
		t.Fatalf("root contexts lost: %+v", parsed.Conditions)
	}
	for i, c := range parsed.Conditions[2:] {
		if c.SequenceStep != 2+i/3 || c.FromUntil != (i >= 3) || c.FromMissing {
			t.Fatalf("copied context %d: %+v", i, c)
		}
	}
	clone := semanticFromExtraction(parsed)
	cp, cu := &clone.Sequence.Steps[2], clone.Sequence.Until
	if cu == nil || cp.Subquery == cu.Subquery || cp.Subquery.Expression == cu.Subquery.Expression || cp.ByKeys[0].Field == cu.ByKeys[0].Field || len(untilRunsSemanticLeaves(clone.Expression)) != 5 {
		t.Fatal("semantic clone reused owners or omitted positive predicate")
	}
	p.Subquery.Conditions[0].Value = "changed"
	p.Subquery.Conditions[0].RHS.Text = "changed"
	p.ByKeys[0].Field.Name = "changed"
	p.Subquery.Expression.Children[0].Operator = BooleanAnd
	if u.Subquery.Conditions[0].Value != "stop" || u.Subquery.Conditions[0].RHS.Text != "stop" || u.ByKeys[0].Field.Name != "process.entity_id" || u.Subquery.Expression.Children[0].Operator != BooleanOr || cp.Subquery.Conditions[0].Value != "stop" || cp.ByKeys[0].Field.Name != "process.entity_id" {
		t.Fatal("positive owner mutation reached final until or semantic clone")
	}
}

func TestUntilRunsUnrecognizedAndInvalidGuards(t *testing.T) {
	for _, modifier := range []string{"runs=0", "runs=101", "runs=999999999999999999999999", "other=2"} {
		t.Run(modifier, func(t *testing.T) {
			p, s := ExtractConditions(untilRunsBase+" with "+modifier), AnalyzeQuery(untilRunsBase+" with "+modifier)
			if len(p.Errors) == 0 || len(s.Errors) == 0 || p.Sequence == nil || s.Sequence == nil || len(p.Sequence.Steps) != 2 || len(s.Sequence.Steps) != 2 || p.Sequence.Until == nil || s.Sequence.Until == nil || p.Sequence.Until.Runs != 0 || s.Sequence.Until.Runs != 0 {
				t.Fatalf("invalid/unknown modifier was normalized: %+v / %+v", p, s)
			}
		})
	}
	for _, query := range []string{
		`join [process where process.name == "start"] [file where file.name == "out"] until [process where event.type == "stop"] with runs=2`,
		`sequence with maxspan=1s [process where true] [file where true] until ![process where event.type == "stop"] with runs=2`,
	} {
		// Characterize the existing non-sequence/missing carrier without
		// claiming vendor validity or changing its parse diagnostics.
		p := ExtractConditions(query)
		if p.Sequence == nil || len(p.Sequence.Steps) != 2 || p.Sequence.Until == nil || p.Sequence.Until.Runs != 0 {
			t.Fatalf("excluded context normalized: %+v", p)
		}
	}
}

func TestUntilRunsSharedBudgetAndCancellation(t *testing.T) {
	query := untilRunsBase + " with runs=100"
	// Four retained root conditions plus four independently extracted
	// one-condition subqueries consume eight shared condition slots, not N.
	for _, allowance := range []int{8, 7} {
		b := newParseBudget(context.Background())
		b.conditions = allowance
		p := extractWithBudget(query, b)
		if allowance == 8 {
			if len(p.Errors) != 0 || p.Sequence == nil || len(p.Sequence.Steps) != 3 || b.conditions != 0 {
				t.Fatalf("exact compact condition accounting: remaining=%d result=%+v", b.conditions, p)
			}
		} else if len(p.Errors) != 1 || !strings.Contains(p.Errors[0], "(conditions)") || len(p.Conditions) != 0 || p.Sequence != nil || p.Expression != nil {
			t.Fatalf("one-short condition failure exposed partial output: %+v", p)
		}
	}
	for _, api := range []string{"extract", "analyze"} {
		for _, resource := range []string{"work", "output"} {
			t.Run(api+"/"+resource, func(t *testing.T) {
				measure := newParseBudget(context.Background())
				if api == "extract" {
					if r := extractWithBudget(query, measure); len(r.Errors) != 0 {
						t.Fatal(r.Errors)
					}
				} else {
					if r := analyzeWithBudget(query, measure); len(r.Errors) != 0 {
						t.Fatal(r.Errors)
					}
				}
				used := maxParseWork - measure.work
				if resource == "output" {
					used = MaxOutputBytes - measure.output
				}
				for _, short := range []int{0, 1} {
					b := newParseBudget(context.Background())
					if resource == "work" {
						b.work = used - short
					} else {
						b.output = used - short
					}
					var errs []string
					var partial bool
					if api == "extract" {
						r := extractWithBudget(query, b)
						errs = r.Errors
						partial = len(r.Conditions) != 0 || r.Sequence != nil || r.Expression != nil
					} else {
						r := analyzeWithBudget(query, b)
						errs = r.Errors
						partial = len(r.Conditions) != 0 || r.Sequence != nil || r.Expression != nil
					}
					if short == 0 && len(errs) != 0 {
						t.Fatalf("exact budget failed: %v", errs)
					}
					if short == 1 && (len(errs) != 1 || !strings.Contains(errs[0], ErrResourceLimit.Error()) || partial) {
						t.Fatalf("one-short failure: errors=%v partial=%v", errs, partial)
					}
				}
			})
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, s := ExtractConditionsContext(ctx, query), AnalyzeQueryContext(ctx, query)
	if len(p.Errors) != 1 || len(s.Errors) != 1 || !strings.Contains(p.Errors[0], context.Canceled.Error()) || !strings.Contains(s.Errors[0], context.Canceled.Error()) || len(p.Conditions)+len(s.Conditions) != 0 || p.Sequence != nil || s.Sequence != nil || p.Expression != nil || s.Expression != nil {
		t.Fatalf("cancellation exposed partial output: %+v / %+v", p, s)
	}
}

func TestUntilRunsInvalidQueryKeepsPartialShape(t *testing.T) {
	for _, tc := range []struct {
		query string
		steps int
	}{
		{`sequence [process where process.name == "start"] until [process where event.type == "stop"] with runs=2`, 1},
		{`sequence [process where process.name == "start"] with other=2 [file where file.name == "out"] until [process where event.type == "stop"] with runs=2`, 2},
	} {
		ast, err := Parse(tc.query)
		if err == nil || ast == nil {
			t.Fatalf("invalid control lost its diagnostic/partial AST: %v", err)
		}
		p, s := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
		if len(ast.Body.(*Sequence).Steps) != tc.steps || len(p.Errors) == 0 || len(s.Errors) == 0 || p.Sequence == nil || s.Sequence == nil || len(p.Sequence.Steps) != tc.steps || len(s.Sequence.Steps) != tc.steps || p.Sequence.Until == nil || s.Sequence.Until == nil || p.Sequence.Until.Runs != 0 || s.Sequence.Until.Runs != 0 {
			t.Errorf("an invalid query gained normalized correlation terms: parse=%+v semantic=%+v", p, s)
		}
	}
}

func TestUntilRunsHistoricalFinalDescriptorBudget(t *testing.T) {
	// Isolate the existing partial-AST descriptor path, without claiming this
	// AST is a valid EQL query. Historically its sole retained charge is the
	// sequence command: 128 + 6*len("sequence") = 176 output, two work checks.
	for _, runs := range []int{0, 1} {
		for _, resource := range []string{"output", "work"} {
			b := newParseBudget(context.Background())
			wantOutput, wantWork := MaxOutputBytes-176, maxParseWork-2
			if resource == "output" {
				b.output, wantOutput = 176, 0
			} else {
				b.work, wantWork = 2, 0
			}
			result := &ParseResult{Conditions: []Condition{}}
			step := &SequenceStep{Runs: runs}
			if runs == 1 {
				step.WithKey = "runs"
			}
			var failed error
			func() {
				defer func() {
					if v := recover(); v != nil {
						failed = recoveredParseError(v)
					}
				}()
				newExtractorWithBudget(result, b).extractSequence(&Sequence{Kind: KindSequence, Until: step})
			}()
			if failed != nil || b.output != wantOutput || b.work != wantWork || result.Sequence == nil || result.Sequence.Until == nil || result.Sequence.Until.Runs != 0 {
				t.Errorf("historical final descriptor charge changed: runs=%d resource=%s error=%v output=%d work=%d", runs, resource, failed, b.output, b.work)
			}
		}
	}
}
