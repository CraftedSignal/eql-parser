package main

import (
	"testing"

	eql "github.com/craftedsignal/eql-parser"
)

func TestSigmaTranslatorRejectsValueAndUnknownNodes(t *testing.T) {
	for _, expression := range []*eql.ConditionExpression{
		{Operator: eql.BooleanValue, Value: &eql.ValuePredicate{Expression: &eql.ValueExpression{Kind: eql.ValueLiteral, Literal: &eql.Operand{Kind: eql.OperandBool, Text: "true"}}}},
		{Operator: "unknown"},
		{Operator: eql.BooleanAnd, Children: []*eql.ConditionExpression{{Operator: eql.BooleanCondition}, {Operator: eql.BooleanValue}}},
	} {
		b := &sigmaConditionBuilder{res: &eql.ParseResult{Conditions: []eql.Condition{{Field: "x", Operator: "==", Value: "a"}}}, det: map[string]any{}, class: transField}
		if text, ok := b.term(expression); ok || text != "" {
			t.Errorf("unsupported value tree translated: %q, %v", text, ok)
		}
	}
}
