package eql

import "context"

// SemanticQuery is the parser-owned semantic view of an EQL query.
// It resolves parser output into stable condition-bearing nodes so callers do
// not need to understand parser internals or condition indexes.
type SemanticQuery struct {
	Conditions      []Condition         `json:"conditions,omitempty"`
	Expression      *SemanticExpression `json:"expression,omitempty"`
	EventCategories []string            `json:"event_categories,omitempty"`
	Sequence        *SemanticSequence   `json:"sequence,omitempty"`
	Pipes           []PipeInfo          `json:"pipes,omitempty"`
	Commands        []string            `json:"commands,omitempty"`
	Fields          []string            `json:"fields,omitempty"`
	JoinKeys        []string            `json:"join_keys,omitempty"`
	Errors          []string            `json:"errors,omitempty"`
}

// SemanticExpression preserves boolean grouping while pointing directly at
// semantic conditions instead of parser-internal condition indexes.
type SemanticExpression struct {
	Operator  BooleanOperator       `json:"operator"`
	Condition *Condition            `json:"condition,omitempty"`
	Children  []*SemanticExpression `json:"children,omitempty"`
}

// SemanticSequence is the parser-owned semantic view of EQL sequence, join,
// and sample correlation metadata.
type SemanticSequence struct {
	Kind      string                 `json:"kind"`
	MaxSpan   string                 `json:"max_span,omitempty"`
	MaxSpanMS int64                  `json:"max_span_ms,omitempty"`
	ByFields  []string               `json:"by_fields,omitempty"`
	Steps     []SemanticSequenceStep `json:"steps"`
	Until     *SemanticSequenceStep  `json:"until,omitempty"`
}

// SemanticSequenceStep is one step in an EQL correlation query.
type SemanticSequenceStep struct {
	EventCategory  string         `json:"event_category"`
	ByFields       []string       `json:"by_fields,omitempty"`
	Runs           int            `json:"runs,omitempty"`
	Missing        bool           `json:"missing,omitempty"`
	ConditionCount int            `json:"condition_count"`
	Subquery       *SemanticQuery `json:"subquery,omitempty"`
}

// AnalyzeQuery parses an EQL query and returns the parser-owned semantic model.
func AnalyzeQuery(query string) *SemanticQuery {
	return AnalyzeQueryContext(context.Background(), query)
}

// AnalyzeQueryContext shares one cooperative deadline and resource budget
// across extraction, semantic expressions, and sequence-subquery conversion.
func AnalyzeQueryContext(ctx context.Context, query string) *SemanticQuery {
	ctx, cancel := context.WithTimeout(ctx, MaxParseTime)
	defer cancel()
	return analyzeWithBudget(query, newParseBudget(ctx))
}

func analyzeWithBudget(query string, budget *parseBudget) (semantic *SemanticQuery) {
	defer func() {
		if value := recover(); value != nil {
			semantic = &SemanticQuery{Errors: []string{recoveredParseError(value).Error()}}
		}
	}()
	result := extractWithBudget(query, budget)
	semantic = semanticFromExtractionWithBudget(result, budget)
	budget.check(0)
	return semantic
}

// semanticFromExtraction normalizes parser extraction output into the
// parser-owned semantic model.
func semanticFromExtraction(result *ParseResult) *SemanticQuery {
	return semanticFromExtractionWithBudget(result, nil)
}

func semanticFromExtractionWithBudget(result *ParseResult, budget *parseBudget) *SemanticQuery {
	budget.node()
	if result == nil {
		return nil
	}
	semantic := &SemanticQuery{
		Conditions:      cloneSemanticConditionsWithBudget(result.Conditions, budget),
		EventCategories: cloneSemanticStringsWithBudget(result.EventCategories, budget),
		Sequence:        semanticSequenceFromParseSequenceWithBudget(result.Sequence, budget),
		Pipes:           cloneSemanticPipesWithBudget(result.Pipes, budget),
		Commands:        cloneSemanticStringsWithBudget(result.Commands, budget),
		Fields:          cloneSemanticStringsWithBudget(result.Fields, budget),
		JoinKeys:        cloneSemanticStringsWithBudget(result.JoinKeys, budget),
		Errors:          cloneSemanticStringsWithBudget(result.Errors, budget),
	}
	semantic.Expression = semanticExpressionFromParseExpressionWithBudget(result.Expression, semantic.Conditions, budget)
	if semantic.Expression == nil {
		semantic.Expression = semanticExpressionFromConditionsWithBudget(semantic.Conditions, budget)
	}
	return semantic
}

func semanticExpressionFromParseExpression(expression *ConditionExpression, conditions []Condition) *SemanticExpression {
	return semanticExpressionFromParseExpressionWithBudget(expression, conditions, nil)
}

func semanticExpressionFromParseExpressionWithBudget(expression *ConditionExpression, conditions []Condition, budget *parseBudget) *SemanticExpression {
	budget.node()
	if expression == nil {
		return nil
	}
	switch expression.Operator {
	case BooleanCondition:
		if expression.ConditionIndex < 0 || expression.ConditionIndex >= len(conditions) {
			return nil
		}
		return &SemanticExpression{
			Operator:  BooleanCondition,
			Condition: &conditions[expression.ConditionIndex],
		}
	case BooleanAnd, BooleanOr:
		converted := &SemanticExpression{Operator: expression.Operator}
		for _, child := range expression.Children {
			childExpression := semanticExpressionFromParseExpressionWithBudget(child, conditions, budget)
			if childExpression == nil {
				return nil
			}
			converted.Children = append(converted.Children, childExpression)
		}
		if len(converted.Children) == 0 {
			return nil
		}
		return converted
	default:
		return nil
	}
}

func semanticExpressionFromConditionsWithBudget(conditions []Condition, budget *parseBudget) *SemanticExpression {
	budget.node()
	if len(conditions) == 0 {
		return nil
	}
	var expression *SemanticExpression
	for index := range conditions {
		budget.node()
		leaf := &SemanticExpression{Operator: BooleanCondition, Condition: &conditions[index]}
		if expression == nil {
			expression = leaf
			continue
		}
		operator := BooleanAnd
		if conditions[index].LogicalOp == "OR" {
			operator = BooleanOr
		}
		budget.node()
		expression = combineSemanticExpressions(operator, expression, leaf)
	}
	return expression
}

func combineSemanticExpressions(operator BooleanOperator, left, right *SemanticExpression) *SemanticExpression {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	combined := left
	if left.Operator != operator || left.Condition != nil {
		combined = &SemanticExpression{Operator: operator, Children: []*SemanticExpression{left}}
	}
	if right.Operator == operator && right.Condition == nil {
		combined.Children = append(combined.Children, right.Children...)
	} else {
		combined.Children = append(combined.Children, right)
	}
	return combined
}

func semanticSequenceFromParseSequenceWithBudget(sequence *SequenceInfo, budget *parseBudget) *SemanticSequence {
	budget.node()
	if sequence == nil {
		return nil
	}
	return &SemanticSequence{
		Kind:      sequence.Kind,
		MaxSpan:   sequence.MaxSpan,
		MaxSpanMS: sequence.MaxSpanMS,
		ByFields:  cloneSemanticStringsWithBudget(sequence.ByFields, budget),
		Steps:     semanticSequenceStepsFromParseStepsWithBudget(sequence.Steps, budget),
		Until:     semanticSequenceStepFromParseStepWithBudget(sequence.Until, budget),
	}
}

func semanticSequenceStepsFromParseSteps(steps []SequenceStepInfo) []SemanticSequenceStep {
	return semanticSequenceStepsFromParseStepsWithBudget(steps, nil)
}

func semanticSequenceStepsFromParseStepsWithBudget(steps []SequenceStepInfo, budget *parseBudget) []SemanticSequenceStep {
	budget.check(1)
	if len(steps) == 0 {
		return nil
	}
	out := make([]SemanticSequenceStep, 0, len(steps))
	for i := range steps {
		out = append(out, *semanticSequenceStepFromParseStepWithBudget(&steps[i], budget))
	}
	return out
}

func semanticSequenceStepFromParseStep(step *SequenceStepInfo) *SemanticSequenceStep {
	return semanticSequenceStepFromParseStepWithBudget(step, nil)
}

func semanticSequenceStepFromParseStepWithBudget(step *SequenceStepInfo, budget *parseBudget) *SemanticSequenceStep {
	budget.node()
	if step == nil {
		return nil
	}
	return &SemanticSequenceStep{
		EventCategory:  step.EventCategory,
		ByFields:       cloneSemanticStringsWithBudget(step.ByFields, budget),
		Runs:           step.Runs,
		Missing:        step.Missing,
		ConditionCount: step.ConditionCount,
		Subquery:       semanticFromExtractionWithBudget(step.Subquery, budget),
	}
}

func cloneSemanticConditionsWithBudget(conditions []Condition, budget *parseBudget) []Condition {
	budget.check(1)
	if len(conditions) == 0 {
		return nil
	}
	out := make([]Condition, len(conditions))
	for i, condition := range conditions {
		budget.node()
		budget.addOutput(len(condition.Field) + len(condition.Operator) + len(condition.Value) + len(condition.EventCategory) + len(condition.LogicalOp) + len(condition.Function) + len(condition.Lineage))
		out[i] = condition
		out[i].Alternatives = cloneSemanticStringsWithBudget(condition.Alternatives, budget)
		out[i].CoFields = cloneSemanticStringsWithBudget(condition.CoFields, budget)
	}
	return out
}

func cloneSemanticPipesWithBudget(pipes []PipeInfo, budget *parseBudget) []PipeInfo {
	budget.check(1)
	if len(pipes) == 0 {
		return nil
	}
	out := make([]PipeInfo, len(pipes))
	for i, pipe := range pipes {
		budget.addOutput(len(pipe.Name))
		out[i] = pipe
		out[i].Args = cloneSemanticStringsWithBudget(pipe.Args, budget)
	}
	return out
}

func cloneSemanticStringsWithBudget(values []string, budget *parseBudget) []string {
	budget.check(1)
	if len(values) == 0 {
		return nil
	}
	for _, value := range values {
		budget.addOutput(len(value))
	}
	return append([]string(nil), values...)
}
