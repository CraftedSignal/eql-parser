package eql

// OperandKind identifies the structure of a condition's normalized RHS.
// The empty kind means absent or unknown; callers must not infer it from Value.
type OperandKind string

const (
	OperandString     OperandKind = "string"
	OperandNumber     OperandKind = "number"
	OperandBool       OperandKind = "bool"
	OperandNull       OperandKind = "null"
	OperandField      OperandKind = "field"
	OperandList       OperandKind = "list"
	OperandExpression OperandKind = "expression"
)

// Operand preserves literal kinds, exact numeric notation, field optionality,
// and ordered list items. Expression text is canonical EQL, not evaluated.
type Operand struct {
	Kind     OperandKind `json:"kind"`
	Text     string      `json:"text,omitempty"`
	Optional bool        `json:"optional,omitempty"`
	Items    []Operand   `json:"items,omitempty"`
}

func operandFromExpr(e Expr, budget *parseBudget) *Operand {
	budget.check(1)
	switch v := unwrap(e).(type) {
	case *Literal:
		switch v.Kind {
		case LitString:
			return &Operand{Kind: OperandString, Text: v.Value}
		case LitNumber:
			return &Operand{Kind: OperandNumber, Text: v.Text}
		case LitBool:
			return &Operand{Kind: OperandBool, Text: literalText(v)}
		case LitNull:
			return &Operand{Kind: OperandNull}
		}
	case *Unary:
		if lit, ok := unwrap(v.X).(*Literal); ok && lit.Kind == LitNumber {
			return &Operand{Kind: OperandNumber, Text: v.Op + lit.Text}
		}
	case *Field:
		if !flatFieldIdentity(v, budget) {
			return &Operand{Kind: OperandExpression, Text: canonicalExprWithBudget(e, budget)}
		}
		return &Operand{Kind: OperandField, Text: v.Name(), Optional: v.Optional}
	}
	return &Operand{Kind: OperandExpression, Text: canonicalExprWithBudget(e, budget)}
}

func listOperand(values []Expr, budget *parseBudget) *Operand {
	budget.check(1)
	// Reserve every list node before allocating the full backing array.
	for range values {
		budget.node()
	}
	out := &Operand{Kind: OperandList, Items: make([]Operand, 0, len(values))}
	for _, value := range values {
		budget.check(1)
		out.Items = append(out.Items, *operandFromExpr(value, budget))
	}
	return out
}

func cloneOperand(value *Operand, budget *parseBudget) *Operand {
	budget.check(1)
	if value == nil {
		return nil
	}
	out := *value
	if value.Items != nil {
		budget.check(1)
		out.Items = make([]Operand, len(value.Items))
		for i := range value.Items {
			budget.check(1)
			out.Items[i] = *cloneOperand(&value.Items[i], budget)
		}
	}
	return &out
}

func operandJSONBound(value *Operand, budget *parseBudget) int {
	budget.check(1)
	if value == nil {
		return 0
	}
	n := jsonTextBound(string(value.Kind), budget) + 11 // {"kind":"..."}
	if value.Text != "" {
		n += jsonTextBound(value.Text, budget) + 10
	}
	if value.Optional {
		n += 16
	}
	if len(value.Items) > 0 {
		n += 11 + len(value.Items) - 1
	}
	for i := range value.Items {
		n += operandJSONBound(&value.Items[i], budget)
	}
	return n
}

func jsonTextBound(value string, budget *parseBudget) int {
	n := 0
	for i := 0; i < len(value); i++ {
		budget.check(1)
		b := value[i]
		if b < 0x20 || b == '"' || b == '\\' || b == '<' || b == '>' || b == '&' || b >= 0x80 {
			n += 6
		} else {
			n++
		}
	}
	return n
}
