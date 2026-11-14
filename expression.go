package eql

import (
	"fmt"
	"strconv"
	"strings"
)

// BooleanOperator represents parsed boolean expression operators.
type BooleanOperator string

const (
	BooleanCondition BooleanOperator = "condition"
	BooleanScalar    BooleanOperator = "scalar"
	BooleanAnd       BooleanOperator = "and"
	BooleanOr        BooleanOperator = "or"
)

// ScalarPredicate preserves an opaque atomic predicate as canonical EQL.
// Syntax excludes contextual NOT; Negated records that NOT's parity. It is not
// evaluated truth and must never be dropped by consumers.
type ScalarPredicate struct {
	Syntax  string `json:"syntax"`
	Negated bool   `json:"negated,omitempty"`
}

// ConditionExpression preserves the complete event-filter boolean predicate.
// ConditionIndex references ParseResult.Conditions.
type ConditionExpression struct {
	Operator       BooleanOperator        `json:"operator"`
	ConditionIndex int                    `json:"condition_index"`
	Children       []*ConditionExpression `json:"children,omitempty"`
	Scalar         *ScalarPredicate       `json:"scalar,omitempty"`
}

func buildConditionExpression(query *Query, conditions []Condition) *ConditionExpression {
	return buildConditionExpressionWithBudget(query, conditions, nil)
}

func buildConditionExpressionWithBudget(query *Query, conditions []Condition, budget *parseBudget, extraction ...map[Expr]conditionRange) *ConditionExpression {
	if query == nil {
		return nil
	}
	builder := &eqlExpressionBuilder{conditions: conditions, budget: budget}
	if len(extraction) > 0 {
		builder.leaves = extraction[0]
	}
	expression := builder.query(query)
	if expression != nil {
		return expression
	}
	return nil
}

type eqlExpressionBuilder struct {
	budget     *parseBudget
	conditions []Condition
	next       int
	leaves     map[Expr]conditionRange
}

func (b *eqlExpressionBuilder) query(query *Query) *ConditionExpression {
	var expression *ConditionExpression
	if eventQuery, ok := query.Body.(*EventQuery); ok {
		expression = b.eventQuery(eventQuery)
	}
	for _, pipe := range query.Pipes {
		if pipe == nil || pipe.Name != "filter" {
			continue
		}
		for _, arg := range pipe.Args {
			expression = combineConditionExpressions(BooleanAnd, expression, b.expr(arg, false))
		}
	}
	return expression
}

func (b *eqlExpressionBuilder) eventQuery(query *EventQuery) *ConditionExpression {
	if query == nil || query.Where == nil {
		return nil
	}
	return b.expr(query.Where, false)
}

func (b *eqlExpressionBuilder) expr(expr Expr, negated bool) *ConditionExpression {
	b.budget.node()
	atom := unwrap(expr)
	switch typed := atom.(type) {
	case *Binary:
		if typed.Op == "and" || typed.Op == "or" {
			operator := BooleanAnd
			if typed.Op == "or" {
				operator = BooleanOr
			}
			if negated {
				if operator == BooleanAnd {
					operator = BooleanOr
				} else {
					operator = BooleanAnd
				}
			}
			return combineConditionExpressions(operator, b.expr(typed.L, negated), b.expr(typed.R, negated))
		}
	case *Not:
		return b.expr(typed.X, !negated)
	}
	// Extraction records all emissions from each AST atom. A scalar may consume
	// flat best-effort conditions, but those indexes are never semantic leaves.
	if faithfulConditionAtom(atom, b.budget) {
		if b.leaves != nil {
			span, ok := b.leaves[atom]
			if !ok {
				span, ok = b.leaves[expr]
			}
			if ok && span.end == span.start+1 {
				return &ConditionExpression{Operator: BooleanCondition, ConditionIndex: span.start}
			}
		} else {
			return b.condition()
		}
	}
	if atom == nil {
		return nil
	}
	syntax := canonicalExprWithBudget(atom, b.budget)
	b.budget.node()
	return &ConditionExpression{Operator: BooleanScalar, Scalar: &ScalarPredicate{Syntax: syntax, Negated: negated}}
}

// Only atoms faithfully represented by a Condition are executable condition
// leaves. Computed operands and calls remain opaque, even if flat extraction
// reports a useful primary field.
func faithfulConditionAtom(e Expr, budget *parseBudget) bool {
	budget.check(1)
	switch v := unwrap(e).(type) {
	case *Field:
		return true
	case *Binary:
		switch v.Op {
		case "==", "!=", "<", "<=", ">", ">=":
		default:
			return false
		}
		_, lf := unwrap(v.L).(*Field)
		_, rf := unwrap(v.R).(*Field)
		return lf && (rf || literalOperand(v.R)) || rf && literalOperand(v.L)
	case *InExpr:
		if _, ok := unwrap(v.X).(*Field); !ok {
			return false
		}
		for _, item := range v.List {
			budget.check(1)
			if !literalOperand(item) {
				return false
			}
		}
		return true
	case *PatternExpr:
		if _, ok := unwrap(v.X).(*Field); !ok {
			return false
		}
		for _, item := range v.Patterns {
			budget.check(1)
			if !literalOperand(item) {
				return false
			}
		}
		return true
	}
	return false
}

func comparisonEmitsCondition(binary *Binary) bool {
	if binary == nil {
		return false
	}
	left, _ := primaryField(binary.L)
	right, _ := primaryField(binary.R)
	return left != nil || right != nil
}

func (b *eqlExpressionBuilder) condition() *ConditionExpression {
	b.budget.node()
	if b.next >= len(b.conditions) {
		return nil
	}
	index := b.next
	b.next++
	return &ConditionExpression{Operator: BooleanCondition, ConditionIndex: index}
}

func remapConditionExpression(expression *ConditionExpression, indexMap []int) *ConditionExpression {
	return remapConditionExpressionWithBudget(expression, indexMap, nil)
}

func remapConditionExpressionWithBudget(expression *ConditionExpression, indexMap []int, budget *parseBudget) *ConditionExpression {
	if expression == nil || len(indexMap) == 0 {
		return expression
	}
	remapper := &conditionExpressionRemapper{indexMap: indexMap, budget: budget, ids: map[*ConditionExpression]int{}, interned: map[string]int{}}
	return remapper.remap(expression)
}

type conditionExpressionRemapper struct {
	indexMap []int
	budget   *parseBudget
	ids      map[*ConditionExpression]int
	interned map[string]int
}

// identity interns a node's operator and direct child identities. Each edge is
// encoded once, so duplicate detection does not rescan earlier subtrees.
func (r *conditionExpressionRemapper) identity(expression *ConditionExpression) int {
	if id, ok := r.ids[expression]; ok {
		return id
	}
	var key strings.Builder
	key.WriteString(string(expression.Operator))
	key.WriteByte(':')
	key.WriteString(strconv.Itoa(expression.ConditionIndex))
	for _, child := range expression.Children {
		r.budget.check(1)
		key.WriteByte(',')
		key.WriteString(strconv.Itoa(r.identity(child)))
	}
	text := key.String()
	id, ok := r.interned[text]
	if !ok {
		id = len(r.interned) + 1
		r.interned[text] = id
	}
	r.ids[expression] = id
	return id
}

func (r *conditionExpressionRemapper) remap(expression *ConditionExpression) *ConditionExpression {
	r.budget.node()
	if expression == nil {
		return nil
	}
	switch expression.Operator {
	case BooleanCondition:
		if expression.ConditionIndex < 0 || expression.ConditionIndex >= len(r.indexMap) {
			return nil
		}
		mapped := r.indexMap[expression.ConditionIndex]
		if mapped < 0 {
			return nil
		}
		return &ConditionExpression{Operator: BooleanCondition, ConditionIndex: mapped}
	case BooleanScalar:
		if expression.Scalar == nil {
			panic(parseAbort{err: fmt.Errorf("scalar predicate has no payload")})
		}
		r.budget.addOutput(len(expression.Scalar.Syntax))
		scalar := *expression.Scalar
		return &ConditionExpression{Operator: BooleanScalar, Scalar: &scalar}
	case BooleanAnd, BooleanOr:
		converted := &ConditionExpression{Operator: expression.Operator}
		seen := map[int]bool{}
		appendChild := func(child *ConditionExpression) {
			r.budget.check(1)
			// Unknown calls need not be pure. Never deduplicate a scalar or
			// a group containing one, even when its syntax repeats.
			if expressionHasOperator(child, BooleanScalar) {
				converted.Children = append(converted.Children, child)
				return
			}
			id := r.identity(child)
			if !seen[id] {
				seen[id] = true
				converted.Children = append(converted.Children, child)
			}
		}
		for _, child := range expression.Children {
			remapped := r.remap(child)
			if remapped == nil {
				continue
			}
			if remapped.Operator == expression.Operator {
				for _, grandchild := range remapped.Children {
					appendChild(grandchild)
				}
			} else {
				appendChild(remapped)
			}
		}
		if len(converted.Children) == 0 {
			return nil
		}
		if len(converted.Children) == 1 {
			return converted.Children[0]
		}
		return converted
	default:
		panic(parseAbort{err: fmt.Errorf("unknown boolean operator %q", expression.Operator)})
	}
}

func equalConditionExpression(a, b *ConditionExpression) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Operator != b.Operator || a.ConditionIndex != b.ConditionIndex || !equalScalarPredicate(a.Scalar, b.Scalar) || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !equalConditionExpression(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

func combineConditionExpressions(operator BooleanOperator, left, right *ConditionExpression) *ConditionExpression {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	// Both inputs are newly built, owned expression trees. Reuse the left
	// group so a flat chain grows one slice instead of copying every prefix.
	combined := left
	if left.Operator != operator {
		combined = &ConditionExpression{Operator: operator, Children: []*ConditionExpression{left}}
	}
	if right.Operator == operator {
		combined.Children = append(combined.Children, right.Children...)
	} else {
		combined.Children = append(combined.Children, right)
	}
	return combined
}

func expressionHasOperator(expression *ConditionExpression, operator BooleanOperator) bool {
	if expression == nil {
		return false
	}
	if expression.Operator == operator {
		return true
	}
	for _, child := range expression.Children {
		if expressionHasOperator(child, operator) {
			return true
		}
	}
	return false
}

func equalScalarPredicate(a, b *ScalarPredicate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
