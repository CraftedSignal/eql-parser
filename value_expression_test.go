package eql

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValueExpressionComparisonWitnesses(t *testing.T) {
	field := &ValueExpression{Kind: ValueField, FieldPath: []ValueFieldSegment{{Name: "process"}, {Name: "name"}}}
	member := &ValueExpression{Kind: ValueMembership, Children: []*ValueExpression{field, wave16ValueLiteral(OperandString, "cmd.exe")}}
	truth := wave16ValueLiteral(OperandBool, "true")
	for _, tc := range []struct {
		query       string
		left, right *ValueExpression
	}{
		{`process where process.name in ("cmd.exe") == true`, member, truth},
		{`process where true == process.name in ("cmd.exe")`, truth, member},
	} {
		p, s := ExtractConditions(tc.query), AnalyzeQuery(tc.query)
		wave16RequireValueModels(t, p, s)
		want := &ValuePredicate{Expression: &ValueExpression{Kind: ValueBinary, Operator: "==", Children: []*ValueExpression{tc.left, tc.right}}}
		if !reflect.DeepEqual(p.Expression.Value, want) || !reflect.DeepEqual(s.Expression.Value, want) || !reflect.DeepEqual(p.Fields, []string{"process.name"}) {
			t.Fatalf("membership comparison lost structure: %+v / %+v", p.Expression.Value, s.Expression.Value)
		}
		encoded, err := json.Marshal(p.Expression)
		var decoded ConditionExpression
		if err != nil || json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(&decoded, p.Expression) {
			t.Fatalf("value JSON round trip changed payload: %s / %v", encoded, err)
		}
	}
}

func TestValueExpressionKindsAndNegation(t *testing.T) {
	query := "any where ?`a.b`[0] not in~ (null, -1, FuN~((true and not false))) == false"
	p, s := ExtractConditions(query), AnalyzeQuery(query)
	wave16RequireValueModels(t, p, s)
	member := p.Expression.Value.Expression.Children[0]
	if member.Kind != ValueMembership || !member.Negated || !member.Insensitive || len(member.Children) != 4 {
		t.Fatalf("membership flags/list order lost: %+v", member)
	}
	f := member.Children[0]
	if f.Kind != ValueField || !f.Optional || !reflect.DeepEqual(f.FieldPath, []ValueFieldSegment{{Name: "a.b"}, {IsIndex: true, Index: 0}}) || member.Children[1].Literal.Kind != OperandNull || member.Children[2].Kind != ValueUnary || member.Children[2].Operator != "-" || member.Children[2].Children[0].Literal.Text != "1" {
		t.Fatalf("literal/path/sign identity lost: %+v", member)
	}
	call := member.Children[3]
	if call.Kind != ValueCall || call.Name != "FuN" || !call.Insensitive || call.Children[0].Kind != ValueGroup || call.Children[0].Children[0].Kind != ValueBinary || call.Children[0].Children[0].Operator != "and" || call.Children[0].Children[0].Children[1].Kind != ValueNot {
		t.Fatalf("call/group/internal NOT lost: %+v", call)
	}
	p, s = ExtractConditions(`any where not (-x like~ ("a*", "b*") == false)`), AnalyzeQuery(`any where not (-x like~ ("a*", "b*") == false)`)
	wave16RequireValueModels(t, p, s)
	root := p.Expression.Value
	pattern := root.Expression.Children[0].Children[0].Children[0]
	if !root.Negated || root.Expression.Kind != ValueGroup || pattern.Kind != ValuePattern || pattern.Operator != "like" || !pattern.Parenthesized || !pattern.Insensitive || pattern.Negated || pattern.Children[1].Literal.Text != "a*" || pattern.Children[2].Literal.Text != "b*" {
		t.Fatalf("outer NOT/pattern shape lost: %+v", root)
	}
	p, s = ExtractConditions(`any where f(child of [process where ?x in~ (null) == true]) == null`), AnalyzeQuery(`any where f(child of [process where ?x in~ (null) == true]) == null`)
	wave16RequireValueModels(t, p, s)
	rel := p.Expression.Value.Expression.Children[0].Children[0]
	if rel.Kind != ValueRelationship || rel.Name != "child" || rel.Event == nil || rel.Event.Category != "process" || rel.Event.CategoryAny || rel.Event.Bare || rel.Event.Where.Kind != ValueBinary || rel.Event.Where.Children[0].Kind != ValueMembership {
		t.Fatalf("relationship event structure lost: %+v", rel)
	}
	q := `any where event.code in (1) == true`
	if result := ExtractConditions(q); len(result.Errors) != 0 || len(result.Conditions) != 0 || GetEventTypeFromConditions(result) != "" || !reflect.DeepEqual(result.Fields, []string{"event.code"}) {
		t.Fatalf("nested Boolean value invented an event-code projection: %+v", result)
	}
}

func TestValueExpressionMixedOwnershipAndNoDedup(t *testing.T) {
	query := `process where x == 1 and (y in (2) == true or y in (2) == true) | filter z in (3) == false`
	p, s := ExtractConditions(query), AnalyzeQuery(query)
	if len(p.Errors)+len(s.Errors) != 0 || len(p.Conditions) != 1 || p.Conditions[0].Field != "x" || p.Expression.Operator != BooleanAnd || len(p.Expression.Children) != 3 || p.Expression.Children[1].Operator != BooleanOr || len(p.Expression.Children[1].Children) != 2 || !reflect.DeepEqual(p.Fields, []string{"x", "y", "z"}) {
		encoded, _ := json.Marshal(p.Expression)
		t.Fatalf("ordinary/value/pipe leaves were dropped or deduplicated: %s; errors=%v/%v fields=%v", encoded, p.Errors, s.Errors, p.Fields)
	}
	left := p.Expression.Children[1].Children[0].Value.Expression
	left.Children[0].Children[0].FieldPath[0].Name = "mutated"
	if p.Expression.Children[1].Children[1].Value.Expression.Children[0].Children[0].FieldPath[0].Name != "y" || s.Expression.Children[1].Children[0].Value.Expression.Children[0].Children[0].FieldPath[0].Name != "y" {
		t.Fatal("value fields alias sibling or semantic payload")
	}
	query = `sequence [process where x in ("a") == true] with runs=2 [file where y == 1]`
	p, s = ExtractConditions(query), AnalyzeQuery(query)
	if len(p.Errors)+len(s.Errors) != 0 || p.Sequence == nil || s.Sequence == nil || len(p.Sequence.Steps) != 2 || p.Sequence.Steps[0].ConditionCount != 0 || p.Sequence.Steps[0].Runs != 2 || len(p.Conditions) != 1 || s.Expression == nil {
		t.Fatalf("sequence value ownership lost: %+v / %+v", p, s)
	}
	parsed := p.Sequence.Steps[0].Subquery.Expression.Value.Expression
	semantic := s.Sequence.Steps[0].Subquery.Expression.Value.Expression
	root := s.Expression.Children[0].Value.Expression
	parsed.Children[0].Children[1].Literal.Text = "mutated"
	if semantic.Children[0].Children[1].Literal.Text != "a" || root.Children[0].Children[1].Literal.Text != "a" {
		t.Fatal("sequence value payload aliases extraction/semantic owners")
	}
	semantic.Children[0].Children[0].FieldPath[0].Name = "mutated"
	if root.Children[0].Children[0].FieldPath[0].Name != "x" {
		t.Fatal("semantic step value aliases root value")
	}
	for _, query := range []string{`any where x in ("a")`, `any where false`, `any where length(x) > 1`} {
		result := ExtractConditions(query)
		if len(result.Errors) != 0 || result.Expression == nil || result.Expression.Operator == BooleanValue {
			t.Fatalf("unrelated legacy condition/scalar contract changed: %+v", result)
		}
	}
}

func TestValueExpressionValidationAndSharedBudgets(t *testing.T) {
	for _, value := range []*ValueExpression{
		{Kind: "unknown"}, {Kind: ValueBinary, Operator: "=="},
		{Kind: ValueLiteral, Literal: &Operand{Kind: OperandExpression, Text: "x"}},
		{Kind: ValueField}, {Kind: ValueGroup, Children: []*ValueExpression{nil}},
		{Kind: ValueLiteral, Literal: &Operand{Kind: OperandNull}, Operator: "=="},
	} {
		func() {
			defer func() {
				if _, ok := recover().(parseAbort); !ok {
					t.Errorf("bad value node was not diagnosed: %+v", value)
				}
			}()
			_ = cloneValuePredicate(&ValuePredicate{Expression: value}, nil)
		}()
	}
	query := `any where ?x in (null, "a") == true`
	for _, resource := range []string{"nodes", "output", "work", "cancel"} {
		ctx, cancel := context.WithCancel(context.Background())
		b := newParseBudget(ctx)
		switch resource {
		case "nodes":
			b.nodes = 1
		case "output":
			b.output = 1
		case "work":
			b.work = 1
		case "cancel":
			cancel()
		}
		p := extractWithBudget(query, b)
		if len(p.Errors) == 0 || len(p.Conditions) != 0 || p.Expression != nil || len(p.Fields) != 0 {
			t.Errorf("%s extraction leaked a partial value model: %+v", resource, p)
		}
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s := AnalyzeQueryContext(ctx, query); len(s.Errors) == 0 || !strings.Contains(s.Errors[0], context.Canceled.Error()) || s.Expression != nil || len(s.Conditions) != 0 {
		t.Fatalf("canceled semantic value leaked: %+v", s)
	}
	ast, err := ParseExpression(`x in ("a") == true`)
	if err != nil {
		t.Fatal(err)
	}
	b := newParseBudget(context.Background())
	value := valueExpressionFromAST(ast, b)
	used := MaxParseNodes - b.nodes
	for _, remaining := range []int{used, used - 1} {
		func() {
			budget := newParseBudget(context.Background())
			budget.nodes = remaining
			defer func() {
				failure := recover()
				if remaining == used && failure != nil {
					t.Errorf("exact clone node budget failed: %v", failure)
				}
				if remaining < used {
					abort, ok := failure.(parseAbort)
					if !ok || !errors.Is(abort.err, ErrResourceLimit) {
						t.Errorf("one-short clone node budget did not fail: %v", failure)
					}
				}
			}()
			_ = cloneValueExpression(value, budget)
		}()
	}
}

func TestValueExpressionEventAndLiteralClone(t *testing.T) {
	query := "any where f(child of [process where ?`x.y`[0] in~ (\"\\u{61}\") == true]) == null"
	p, s := ExtractConditions(query), AnalyzeQuery(query)
	wave16RequireValueModels(t, p, s)
	event := p.Expression.Value.Expression.Children[0].Children[0].Event
	other := s.Expression.Value.Expression.Children[0].Children[0].Event
	if event.Where.Children[0].Children[1].Literal.Text != "a" {
		t.Fatalf("literal was not decoded: %+v", event.Where.Children[0].Children[1].Literal)
	}
	event.Category = "changed"
	event.Where.Children[0].Children[1].Literal.Text = "changed"
	event.Where.Children[0].Children[0].FieldPath[0].Name = "changed"
	if other.Category != "process" || other.Where.Children[0].Children[1].Literal.Text != "a" || other.Where.Children[0].Children[0].FieldPath[0].Name != "x.y" {
		t.Fatal("event/literal/path records alias semantic owner")
	}
	for _, original := range []*EventQuery{
		{Category: "process", Where: &Literal{Kind: LitBool, Bool: true}},
		{CategoryAny: true, Where: &Literal{Kind: LitBool, Bool: true}},
		{Bare: true, Where: &Literal{Kind: LitBool, Bool: true}},
	} {
		value := valueExpressionFromAST(&Lineage{Kind: "child", Sub: original}, nil)
		if value.Event.Category != original.Category || value.Event.CategoryAny != original.CategoryAny || value.Event.Bare != original.Bare {
			t.Fatal("relationship event flags changed")
		}
	}
}

func wave16ValueLiteral(kind OperandKind, text string) *ValueExpression {
	return &ValueExpression{Kind: ValueLiteral, Literal: &Operand{Kind: kind, Text: text}}
}

func wave16RequireValueModels(t *testing.T, p *ParseResult, s *SemanticQuery) {
	t.Helper()
	if len(p.Errors)+len(s.Errors) != 0 || len(p.Conditions)+len(s.Conditions) != 0 || p.Expression == nil || s.Expression == nil || p.Expression.Operator != BooleanValue || s.Expression.Operator != BooleanValue || p.Expression.Value == nil || s.Expression.Value == nil || p.Expression.Scalar != nil || s.Expression.Scalar != nil || len(p.Expression.Children)+len(s.Expression.Children) != 0 || s.Expression.Condition != nil {
		t.Fatalf("expected authoritative value leaves without flat/scalar fallback: %+v / %+v", p, s)
	}
	if !reflect.DeepEqual(p.Expression.Value, s.Expression.Value) {
		t.Fatal("parse/semantic value payloads differ")
	}
}

func TestValueExpressionEmptyQuotedField(t *testing.T) {
	query := "any where (`` in (\"x\")) == true"
	p, s := ExtractConditions(query), AnalyzeQuery(query)
	wave16RequireValueModels(t, p, s)
	want := &ValuePredicate{Expression: &ValueExpression{Kind: ValueBinary, Operator: "==", Children: []*ValueExpression{
		{Kind: ValueGroup, Children: []*ValueExpression{{Kind: ValueMembership, Children: []*ValueExpression{
			{Kind: ValueField, FieldPath: []ValueFieldSegment{{Name: "", IsIndex: false}}},
			wave16ValueLiteral(OperandString, "x"),
		}}}},
		wave16ValueLiteral(OperandBool, "true"),
	}}}
	if !reflect.DeepEqual(p.Expression.Value, want) || !reflect.DeepEqual(s.Expression.Value, want) {
		t.Fatalf("empty named segment identity lost: %+v / %+v", p.Expression.Value, s.Expression.Value)
	}
	encoded, err := json.Marshal(p.Expression.Value)
	var decoded ValuePredicate
	if err != nil || json.Unmarshal(encoded, &decoded) != nil || !reflect.DeepEqual(&decoded, want) {
		t.Fatalf("empty named segment JSON identity lost: %s / %v", encoded, err)
	}
	cloned := cloneValuePredicate(p.Expression.Value, nil)
	p.Expression.Value.Expression.Children[0].Children[0].Children[0].FieldPath[0].Name = "mutated"
	if !reflect.DeepEqual(cloned, want) || !reflect.DeepEqual(s.Expression.Value, want) {
		t.Fatal("empty named segment path aliases clone or semantic owner")
	}
}

func TestValueExpressionFieldSegmentValidation(t *testing.T) {
	for _, path := range [][]ValueFieldSegment{
		nil,
		{{Name: "x", IsIndex: true}},
		{{Name: "x", Index: 1}},
	} {
		func() {
			defer func() {
				if _, ok := recover().(parseAbort); !ok {
					t.Errorf("malformed field path was not diagnosed: %+v", path)
				}
			}()
			_ = cloneValueExpression(&ValueExpression{Kind: ValueField, FieldPath: path}, nil)
		}()
	}
}
