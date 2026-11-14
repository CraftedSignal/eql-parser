package eql

import (
	"strconv"
	"strings"
)

// BooleanOperator represents parsed boolean expression operators.
type BooleanOperator string

const (
	BooleanCondition BooleanOperator = "condition"
	BooleanAnd       BooleanOperator = "and"
	BooleanOr        BooleanOperator = "or"
)

// ConditionExpression preserves boolean grouping over extracted Conditions.
// ConditionIndex references ParseResult.Conditions.
type ConditionExpression struct {
	Operator       BooleanOperator        `json:"operator"`
	ConditionIndex int                    `json:"condition_index"`
	Children       []*ConditionExpression `json:"children,omitempty"`
}

func buildConditionExpression(query *Query, conditions []Condition) *ConditionExpression {
	return buildConditionExpressionWithBudget(query, conditions, nil)
}

func buildConditionExpressionWithBudget(query *Query, conditions []Condition, budget *parseBudget) *ConditionExpression {
	if query == nil || len(conditions) == 0 {
		return nil
	}
	builder := &eqlExpressionBuilder{conditions: conditions, budget: budget}
	expression := builder.query(query)
	if expression != nil && builder.next == len(conditions) {
		return expression
	}
	return nil
}

type eqlExpressionBuilder struct {
	budget     *parseBudget
	conditions []Condition
	next       int
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
	switch typed := unwrap(expr).(type) {
	case *Binary:
		switch typed.Op {
		case "and", "or":
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
		case "==", "!=", "<", "<=", ">", ">=":
			if !comparisonEmitsCondition(typed) {
				return nil
			}
			return b.condition()
		default:
			return nil
		}
	case *Not:
		return b.expr(typed.X, !negated)
	case *Unary:
		return b.expr(typed.X, negated)
	case *InExpr:
		if primary, _ := primaryFieldWithBudget(typed.X, b.budget); primary == nil {
			return nil
		}
		return b.condition()
	case *PatternExpr:
		if primary, _ := primaryFieldWithBudget(typed.X, b.budget); primary == nil {
			return nil
		}
		return b.condition()
	case *Call:
		if primary, _ := primaryFieldWithBudget(&Paren{X: argsAsExpr(typed)}, b.budget); primary == nil {
			return nil
		}
		return b.condition()
	case *Field:
		return b.condition()
	case *Lineage:
		return b.eventQuery(typed.Sub)
	default:
		return nil
	}
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
	case BooleanAnd, BooleanOr:
		converted := &ConditionExpression{Operator: expression.Operator}
		seen := map[int]bool{}
		appendChild := func(child *ConditionExpression) {
			r.budget.check(1)
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
		return nil
	}
}

func equalConditionExpression(a, b *ConditionExpression) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Operator != b.Operator || a.ConditionIndex != b.ConditionIndex || len(a.Children) != len(b.Children) {
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
