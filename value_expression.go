package eql

import (
	"fmt"
	"reflect"
)

// ValueExpressionKind discriminates the payload of a structured value node.
type ValueExpressionKind string

const (
	ValueLiteral      ValueExpressionKind = "literal"
	ValueField        ValueExpressionKind = "field"
	ValueGroup        ValueExpressionKind = "group"
	ValueNot          ValueExpressionKind = "not"
	ValueUnary        ValueExpressionKind = "unary"
	ValueBinary       ValueExpressionKind = "binary"
	ValueCall         ValueExpressionKind = "call"
	ValueMembership   ValueExpressionKind = "membership"
	ValuePattern      ValueExpressionKind = "pattern"
	ValueRelationship ValueExpressionKind = "relationship"
)

// ValuePredicate is an unevaluated atomic value tree. Contextual NOT is kept
// independently of internal NOT and NOT-IN, without Boolean reductions.
type ValuePredicate struct {
	Expression *ValueExpression `json:"expression"`
	Negated    bool             `json:"negated,omitempty"`
}

// ValueFieldSegment preserves a field name or an array index, including zero.
type ValueFieldSegment struct {
	Name    string `json:"name,omitempty"`
	Index   int    `json:"index,omitempty"`
	IsIndex bool   `json:"is_index,omitempty"`
}

// ValueExpression is unevaluated syntax with ordered, recursively typed
// children. Only payload fields applicable to Kind are populated.
type ValueExpression struct {
	Kind          ValueExpressionKind `json:"kind"`
	Literal       *Operand            `json:"literal,omitempty"`
	FieldPath     []ValueFieldSegment `json:"field_path,omitempty"`
	Optional      bool                `json:"optional,omitempty"`
	Operator      string              `json:"operator,omitempty"`
	Name          string              `json:"name,omitempty"`
	Children      []*ValueExpression  `json:"children,omitempty"`
	Insensitive   bool                `json:"insensitive,omitempty"`
	Negated       bool                `json:"negated,omitempty"`
	Parenthesized bool                `json:"parenthesized,omitempty"`
	Event         *ValueEventQuery    `json:"event,omitempty"`
}

// ValueEventQuery retains the event operand of a legacy relationship node.
type ValueEventQuery struct {
	Category    string           `json:"category,omitempty"`
	CategoryAny bool             `json:"category_any,omitempty"`
	Bare        bool             `json:"bare,omitempty"`
	Where       *ValueExpression `json:"where"`
}

func valueExpressionFailure(message string) {
	panic(parseAbort{err: fmt.Errorf("invalid value expression: %s", message)})
}

// These string/slot reservations are local: no ancestor rescans a subtree to
// compute its size. Every retained occurrence and clone owns its own charge.
func valueExpressionText(text string, budget *parseBudget) string {
	budget.addOutputExact(jsonTextBound(text, budget))
	return text
}

func valueExpressionChildren(count int, budget *parseBudget) []*ValueExpression {
	if count == 0 {
		return nil
	}
	for i := 0; i < count; i++ {
		budget.node()
	}
	return make([]*ValueExpression, count)
}

func valueExpressionFromAST(expression Expr, budget *parseBudget) *ValueExpression {
	budget.node()
	out := &ValueExpression{}
	child := func(index int, expression Expr) {
		budget.check(1)
		out.Children[index] = valueExpressionFromAST(expression, budget)
	}
	switch node := expression.(type) {
	case *Literal:
		if node == nil {
			valueExpressionFailure("nil literal")
		}
		budget.node()
		literal := &Operand{}
		switch node.Kind {
		case LitString:
			literal.Kind, literal.Text = OperandString, valueExpressionText(node.Value, budget)
		case LitNumber:
			literal.Kind, literal.Text = OperandNumber, valueExpressionText(node.Text, budget)
		case LitBool:
			literal.Kind, literal.Text = OperandBool, valueExpressionText(literalText(node), budget)
		case LitNull:
			literal.Kind = OperandNull
		default:
			valueExpressionFailure("unknown literal kind")
		}
		out.Kind, out.Literal = ValueLiteral, literal
	case *Field:
		if node == nil {
			valueExpressionFailure("nil field")
		}
		out.Kind, out.Optional = ValueField, node.Optional
		for range node.Path {
			budget.node()
		}
		out.FieldPath = make([]ValueFieldSegment, len(node.Path))
		for i, segment := range node.Path {
			budget.check(1)
			out.FieldPath[i] = ValueFieldSegment{Name: valueExpressionText(segment.Name, budget), Index: segment.Index, IsIndex: segment.IsIndex}
		}
	case *Paren:
		if node == nil {
			valueExpressionFailure("nil group")
		}
		out.Kind, out.Children = ValueGroup, valueExpressionChildren(1, budget)
		child(0, node.X)
	case *Not:
		if node == nil {
			valueExpressionFailure("nil NOT")
		}
		out.Kind, out.Children = ValueNot, valueExpressionChildren(1, budget)
		child(0, node.X)
	case *Unary:
		if node == nil {
			valueExpressionFailure("nil unary")
		}
		out.Kind, out.Operator, out.Children = ValueUnary, valueExpressionText(node.Op, budget), valueExpressionChildren(1, budget)
		child(0, node.X)
	case *Binary:
		if node == nil {
			valueExpressionFailure("nil binary")
		}
		out.Kind, out.Operator, out.Children = ValueBinary, valueExpressionText(node.Op, budget), valueExpressionChildren(2, budget)
		child(0, node.L)
		child(1, node.R)
	case *Call:
		if node == nil {
			valueExpressionFailure("nil call")
		}
		out.Kind, out.Name, out.Insensitive = ValueCall, valueExpressionText(node.Name, budget), node.Insensitive
		out.Children = valueExpressionChildren(len(node.Args), budget)
		for i, arg := range node.Args {
			child(i, arg)
		}
	case *InExpr:
		if node == nil {
			valueExpressionFailure("nil membership")
		}
		out.Kind, out.Negated, out.Insensitive = ValueMembership, node.Negated, node.Insensitive
		out.Children = valueExpressionChildren(1+len(node.List), budget)
		child(0, node.X)
		for i, item := range node.List {
			child(i+1, item)
		}
	case *PatternExpr:
		if node == nil {
			valueExpressionFailure("nil pattern")
		}
		out.Kind, out.Operator = ValuePattern, valueExpressionText(string(node.Kind), budget)
		out.Insensitive, out.Parenthesized = node.Insensitive, node.Parenthesized
		out.Children = valueExpressionChildren(1+len(node.Patterns), budget)
		child(0, node.X)
		for i, pattern := range node.Patterns {
			child(i+1, pattern)
		}
	case *Lineage:
		if node == nil || node.Sub == nil {
			valueExpressionFailure("missing relationship event")
		}
		out.Kind, out.Name = ValueRelationship, valueExpressionText(node.Kind, budget)
		budget.node()
		out.Event = &ValueEventQuery{Category: valueExpressionText(node.Sub.Category, budget), CategoryAny: node.Sub.CategoryAny, Bare: node.Sub.Bare}
		budget.check(1)
		out.Event.Where = valueExpressionFromAST(node.Sub.Where, budget)
	default:
		valueExpressionFailure("unknown or incomplete AST node")
	}
	validateValueExpressionNode(out, budget)
	return out
}

func validateValueExpressionNode(value *ValueExpression, budget *parseBudget) {
	budget.check(1)
	if value == nil {
		valueExpressionFailure("missing node")
	}
	k := value.Kind
	if value.Literal != nil && k != ValueLiteral || len(value.FieldPath) != 0 && k != ValueField || value.Optional && k != ValueField || value.Operator != "" && k != ValueUnary && k != ValueBinary && k != ValuePattern || value.Name != "" && k != ValueCall && k != ValueRelationship || value.Insensitive && k != ValueCall && k != ValueMembership && k != ValuePattern || value.Negated && k != ValueMembership || value.Parenthesized && k != ValuePattern || value.Event != nil && k != ValueRelationship {
		valueExpressionFailure("payload does not match kind")
	}
	n := len(value.Children)
	switch k {
	case ValueLiteral:
		literal := value.Literal
		if literal == nil || literal.Optional || len(literal.Items) != 0 || n != 0 {
			valueExpressionFailure("invalid literal payload")
		}
		switch literal.Kind {
		case OperandString:
		case OperandNumber:
			if literal.Text == "" {
				valueExpressionFailure("empty number")
			}
		case OperandBool:
			if literal.Text != "true" && literal.Text != "false" {
				valueExpressionFailure("invalid Boolean literal")
			}
		case OperandNull:
			if literal.Text != "" {
				valueExpressionFailure("null has text")
			}
		default:
			valueExpressionFailure("non-scalar literal")
		}
	case ValueField:
		if len(value.FieldPath) == 0 || n != 0 {
			valueExpressionFailure("invalid field path")
		}
		for _, segment := range value.FieldPath {
			budget.check(1)
			if segment.IsIndex && segment.Name != "" || !segment.IsIndex && segment.Index != 0 {
				valueExpressionFailure("invalid field segment")
			}
		}
	case ValueGroup, ValueNot:
		if n != 1 {
			valueExpressionFailure("invalid unary arity")
		}
	case ValueUnary:
		if n != 1 || value.Operator != "+" && value.Operator != "-" {
			valueExpressionFailure("invalid arithmetic unary")
		}
	case ValueBinary:
		if n != 2 {
			valueExpressionFailure("invalid binary arity")
		}
		switch value.Operator {
		case "+", "-", "*", "/", "%", "==", "!=", "<", "<=", ">", ">=", "and", "or":
		default:
			valueExpressionFailure("unknown binary operator")
		}
	case ValueCall:
		if value.Name == "" {
			valueExpressionFailure("missing call name")
		}
	case ValueMembership:
		if n < 2 {
			valueExpressionFailure("empty membership")
		}
	case ValuePattern:
		if n < 2 || value.Operator != ":" && value.Operator != "like" && value.Operator != "regex" {
			valueExpressionFailure("invalid pattern")
		}
		for _, pattern := range value.Children[1:] {
			budget.check(1)
			if pattern == nil || pattern.Kind != ValueLiteral {
				valueExpressionFailure("nonliteral pattern")
			}
		}
	case ValueRelationship:
		if n != 0 || value.Name != "child" && value.Name != "descendant" && value.Name != "event" || value.Event == nil || value.Event.Where == nil {
			valueExpressionFailure("invalid relationship")
		}
	default:
		valueExpressionFailure("unknown kind")
	}
	for _, child := range value.Children {
		budget.check(1)
		if child == nil {
			valueExpressionFailure("missing child")
		}
	}
}

func cloneValuePredicate(value *ValuePredicate, budget *parseBudget) *ValuePredicate {
	budget.node()
	if value == nil || value.Expression == nil {
		valueExpressionFailure("missing predicate payload")
	}
	return &ValuePredicate{Expression: cloneValueExpression(value.Expression, budget), Negated: value.Negated}
}

func cloneValueExpression(value *ValueExpression, budget *parseBudget) *ValueExpression {
	validateValueExpressionNode(value, budget)
	budget.node()
	out := &ValueExpression{Kind: value.Kind, Optional: value.Optional, Insensitive: value.Insensitive, Negated: value.Negated, Parenthesized: value.Parenthesized}
	if value.Operator != "" {
		out.Operator = valueExpressionText(value.Operator, budget)
	}
	if value.Name != "" {
		out.Name = valueExpressionText(value.Name, budget)
	}
	if value.Literal != nil {
		budget.node()
		out.Literal = &Operand{Kind: value.Literal.Kind, Text: valueExpressionText(value.Literal.Text, budget)}
	}
	if len(value.FieldPath) != 0 {
		for range value.FieldPath {
			budget.node()
		}
		out.FieldPath = make([]ValueFieldSegment, len(value.FieldPath))
		for i, segment := range value.FieldPath {
			budget.check(1)
			out.FieldPath[i] = ValueFieldSegment{Name: valueExpressionText(segment.Name, budget), Index: segment.Index, IsIndex: segment.IsIndex}
		}
	}
	out.Children = valueExpressionChildren(len(value.Children), budget)
	for i, child := range value.Children {
		budget.check(1)
		out.Children[i] = cloneValueExpression(child, budget)
	}
	if value.Event != nil {
		budget.node()
		out.Event = &ValueEventQuery{Category: valueExpressionText(value.Event.Category, budget), CategoryAny: value.Event.CategoryAny, Bare: value.Event.Bare}
		budget.check(1)
		out.Event.Where = cloneValueExpression(value.Event.Where, budget)
	}
	return out
}

func equalValuePredicate(left, right *ValuePredicate) bool { return reflect.DeepEqual(left, right) }

// Only this new atomic family suppresses the historical flat projections.
// Boolean composition/contextual NOT remain the existing expression walk.
func valuePredicateAtom(expression Expr, budget *parseBudget) bool {
	atom := unwrap(expression)
	switch node := atom.(type) {
	case *Binary:
		if node.Op == "and" || node.Op == "or" {
			return false
		}
	case *Not:
		return false
	case *InExpr:
		if compoundValueOperand(node.X, budget) {
			return true
		}
		for _, child := range node.List {
			if compoundValueOperand(child, budget) {
				return true
			}
		}
		return false
	case *PatternExpr:
		if compoundValueOperand(node.X, budget) {
			return true
		}
		for _, child := range node.Patterns {
			if compoundValueOperand(child, budget) {
				return true
			}
		}
		return false
	}
	return containsValuePredicate(atom, budget)
}

func compoundValueOperand(expression Expr, budget *parseBudget) bool {
	budget.check(1)
	switch node := unwrap(expression).(type) {
	case *Field, *Literal:
		return false
	case *Unary:
		return !literalOperand(node)
	default:
		return true
	}
}

func containsValuePredicate(expression Expr, budget *parseBudget) bool {
	budget.check(1)
	switch node := expression.(type) {
	case *InExpr, *PatternExpr:
		return true
	case *Paren:
		return containsValuePredicate(node.X, budget)
	case *Not:
		return containsValuePredicate(node.X, budget)
	case *Unary:
		return containsValuePredicate(node.X, budget)
	case *Binary:
		return containsValuePredicate(node.L, budget) || containsValuePredicate(node.R, budget)
	case *Call:
		for _, child := range node.Args {
			if containsValuePredicate(child, budget) {
				return true
			}
		}
	case *Lineage:
		return node.Sub != nil && containsValuePredicate(node.Sub.Where, budget)
	}
	return false
}
