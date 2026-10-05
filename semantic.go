package eql

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
	return semanticFromExtraction(ExtractConditions(query))
}

// semanticFromExtraction normalizes parser extraction output into the
// parser-owned semantic model.
func semanticFromExtraction(result *ParseResult) *SemanticQuery {
	if result == nil {
		return nil
	}
	semantic := &SemanticQuery{
		Conditions:      cloneSemanticConditions(result.Conditions),
		EventCategories: cloneSemanticStrings(result.EventCategories),
		Sequence:        semanticSequenceFromParseSequence(result.Sequence),
		Pipes:           cloneSemanticPipes(result.Pipes),
		Commands:        cloneSemanticStrings(result.Commands),
		Fields:          cloneSemanticStrings(result.Fields),
		JoinKeys:        cloneSemanticStrings(result.JoinKeys),
		Errors:          cloneSemanticStrings(result.Errors),
	}
	semantic.Expression = semanticExpressionFromParseExpression(result.Expression, semantic.Conditions)
	if semantic.Expression == nil {
		semantic.Expression = semanticExpressionFromConditions(semantic.Conditions)
	}
	return semantic
}

func semanticExpressionFromParseExpression(expression *ConditionExpression, conditions []Condition) *SemanticExpression {
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
			childExpression := semanticExpressionFromParseExpression(child, conditions)
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

func semanticExpressionFromConditions(conditions []Condition) *SemanticExpression {
	if len(conditions) == 0 {
		return nil
	}
	var expression *SemanticExpression
	for index := range conditions {
		leaf := &SemanticExpression{Operator: BooleanCondition, Condition: &conditions[index]}
		if expression == nil {
			expression = leaf
			continue
		}
		operator := BooleanAnd
		if conditions[index].LogicalOp == "OR" {
			operator = BooleanOr
		}
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
	combined := &SemanticExpression{Operator: operator}
	if left.Operator == operator && left.Condition == nil {
		combined.Children = append(combined.Children, left.Children...)
	} else {
		combined.Children = append(combined.Children, left)
	}
	if right.Operator == operator && right.Condition == nil {
		combined.Children = append(combined.Children, right.Children...)
	} else {
		combined.Children = append(combined.Children, right)
	}
	return combined
}

func semanticSequenceFromParseSequence(sequence *SequenceInfo) *SemanticSequence {
	if sequence == nil {
		return nil
	}
	return &SemanticSequence{
		Kind:      sequence.Kind,
		MaxSpan:   sequence.MaxSpan,
		MaxSpanMS: sequence.MaxSpanMS,
		ByFields:  cloneSemanticStrings(sequence.ByFields),
		Steps:     semanticSequenceStepsFromParseSteps(sequence.Steps),
		Until:     semanticSequenceStepFromParseStep(sequence.Until),
	}
}

func semanticSequenceStepsFromParseSteps(steps []SequenceStepInfo) []SemanticSequenceStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]SemanticSequenceStep, 0, len(steps))
	for i := range steps {
		out = append(out, *semanticSequenceStepFromParseStep(&steps[i]))
	}
	return out
}

func semanticSequenceStepFromParseStep(step *SequenceStepInfo) *SemanticSequenceStep {
	if step == nil {
		return nil
	}
	return &SemanticSequenceStep{
		EventCategory:  step.EventCategory,
		ByFields:       cloneSemanticStrings(step.ByFields),
		Runs:           step.Runs,
		Missing:        step.Missing,
		ConditionCount: step.ConditionCount,
		Subquery:       semanticFromExtraction(step.Subquery),
	}
}

func cloneSemanticConditions(conditions []Condition) []Condition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]Condition, len(conditions))
	for i, condition := range conditions {
		out[i] = condition
		out[i].Alternatives = cloneSemanticStrings(condition.Alternatives)
		out[i].CoFields = cloneSemanticStrings(condition.CoFields)
	}
	return out
}

func cloneSemanticPipes(pipes []PipeInfo) []PipeInfo {
	if len(pipes) == 0 {
		return nil
	}
	out := make([]PipeInfo, len(pipes))
	for i, pipe := range pipes {
		out[i] = pipe
		out[i].Args = cloneSemanticStrings(pipe.Args)
	}
	return out
}

func cloneSemanticStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}
