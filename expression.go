package eql

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
	if query == nil || len(conditions) < 2 {
		return nil
	}
	builder := &eqlExpressionBuilder{conditions: conditions}
	expression := builder.query(query)
	if expression != nil && builder.next == len(conditions) && expressionHasOperator(expression, BooleanOr) {
		return expression
	}
	return nil
}

type eqlExpressionBuilder struct {
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
		if primary, _ := primaryField(typed.X); primary == nil {
			return nil
		}
		return b.condition()
	case *PatternExpr:
		if primary, _ := primaryField(typed.X); primary == nil {
			return nil
		}
		return b.condition()
	case *Call:
		if primary, _ := primaryField(&Paren{X: argsAsExpr(typed)}); primary == nil {
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
	if b.next >= len(b.conditions) {
		return nil
	}
	index := b.next
	b.next++
	return &ConditionExpression{Operator: BooleanCondition, ConditionIndex: index}
}

func remapConditionExpression(expression *ConditionExpression, indexMap []int) *ConditionExpression {
	if expression == nil || len(indexMap) == 0 {
		return expression
	}
	switch expression.Operator {
	case BooleanCondition:
		if expression.ConditionIndex < 0 || expression.ConditionIndex >= len(indexMap) {
			return nil
		}
		mapped := indexMap[expression.ConditionIndex]
		if mapped < 0 {
			return nil
		}
		return &ConditionExpression{Operator: BooleanCondition, ConditionIndex: mapped}
	case BooleanAnd, BooleanOr:
		converted := &ConditionExpression{Operator: expression.Operator}
		for _, child := range expression.Children {
			remapped := remapConditionExpression(child, indexMap)
			if remapped == nil {
				continue
			}
			if remapped.Operator == expression.Operator {
				for _, grandchild := range remapped.Children {
					converted.Children = appendUniqueConditionExpression(converted.Children, grandchild)
				}
				continue
			}
			converted.Children = appendUniqueConditionExpression(converted.Children, remapped)
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

func appendUniqueConditionExpression(children []*ConditionExpression, child *ConditionExpression) []*ConditionExpression {
	for _, existing := range children {
		if equalConditionExpression(existing, child) {
			return children
		}
	}
	return append(children, child)
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
	combined := &ConditionExpression{Operator: operator}
	if left.Operator == operator {
		combined.Children = append(combined.Children, left.Children...)
	} else {
		combined.Children = append(combined.Children, left)
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
