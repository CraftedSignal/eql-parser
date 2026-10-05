package eql

import "testing"

func TestAnalyzeQueryBuildsConditionBearingSemanticExpression(t *testing.T) {
	semantic := AnalyzeQuery(`process where (process.command_line : "*whoami*" or process.executable : "*\\cmd.exe") and not user.name == "SYSTEM"`)
	if semantic == nil {
		t.Fatal("expected semantic query")
	}
	if len(semantic.Errors) > 0 {
		t.Fatalf("unexpected parser errors: %v", semantic.Errors)
	}
	if len(semantic.Conditions) != 3 {
		t.Fatalf("conditions = %#v, want 3", semantic.Conditions)
	}
	if len(semantic.EventCategories) != 1 || semantic.EventCategories[0] != "process" {
		t.Fatalf("event categories = %#v, want process", semantic.EventCategories)
	}
	if semantic.Expression == nil || semantic.Expression.Operator != BooleanAnd {
		t.Fatalf("expression = %#v, want AND root", semantic.Expression)
	}
	if len(semantic.Expression.Children) != 2 {
		t.Fatalf("AND children = %#v, want 2", semantic.Expression.Children)
	}
	left := semantic.Expression.Children[0]
	if left.Operator != BooleanOr || len(left.Children) != 2 {
		t.Fatalf("left expression = %#v, want OR with two children", left)
	}
	if got := left.Children[0].Condition.Field; got != "process.command_line" {
		t.Fatalf("left first field = %q, want process.command_line", got)
	}
	if got := left.Children[1].Condition.Field; got != "process.executable" {
		t.Fatalf("left second field = %q, want process.executable", got)
	}
	right := semantic.Expression.Children[1]
	if right.Operator != BooleanCondition || right.Condition == nil || right.Condition.Field != "user.name" || !right.Condition.Negated {
		t.Fatalf("right expression = %#v, want negated user.name condition", right)
	}
}

func TestSemanticInternalExtractionBuildsLogicalOpsAndClones(t *testing.T) {
	result := &ParseResult{
		Conditions: []Condition{
			{Field: "process.command_line", Operator: ":", Value: "*whoami*", Alternatives: []string{"*whoami*", "*hostname*"}, CoFields: []string{"process.args"}},
			{Field: "process.executable", Operator: ":", Value: "*cmd.exe", LogicalOp: "OR"},
		},
		EventCategories: []string{"process"},
		Pipes:           []PipeInfo{{Name: "filter", Args: []string{`user.name == "alice"`}}},
		Commands:        []string{"process", "filter"},
		Fields:          []string{"process.command_line"},
		JoinKeys:        []string{"host.id"},
	}

	semantic := semanticFromExtraction(result)
	if semantic == nil {
		t.Fatal("expected semantic query")
	}
	if semantic.Expression == nil || semantic.Expression.Operator != BooleanOr || len(semantic.Expression.Children) != 2 {
		t.Fatalf("internal expression = %#v, want OR over two conditions", semantic.Expression)
	}

	result.Conditions[0].Field = "changed"
	result.Conditions[0].Alternatives[0] = "changed"
	result.Conditions[0].CoFields[0] = "changed"
	result.EventCategories[0] = "changed"
	result.Pipes[0].Args[0] = "changed"
	result.Commands[0] = "changed"
	result.Fields[0] = "changed"
	result.JoinKeys[0] = "changed"
	if semantic.Conditions[0].Field != "process.command_line" ||
		semantic.Conditions[0].Alternatives[0] != "*whoami*" ||
		semantic.Conditions[0].CoFields[0] != "process.args" ||
		semantic.EventCategories[0] != "process" ||
		semantic.Pipes[0].Args[0] != `user.name == "alice"` ||
		semantic.Commands[0] != "process" ||
		semantic.Fields[0] != "process.command_line" ||
		semantic.JoinKeys[0] != "host.id" {
		t.Fatalf("semantic query did not clone parse result: %#v", semantic)
	}
}

func TestSemanticInternalExtractionPreservesNestedSequenceSubqueries(t *testing.T) {
	semantic := semanticFromExtraction(&ParseResult{
		Sequence: &SequenceInfo{
			Kind:     "sequence",
			MaxSpan:  "5m",
			ByFields: []string{"host.id"},
			Steps: []SequenceStepInfo{{
				EventCategory:  "process",
				ByFields:       []string{"user.name"},
				Runs:           2,
				ConditionCount: 1,
				Subquery: &ParseResult{
					EventCategories: []string{"process"},
					Conditions:      []Condition{{Field: "process.name", Operator: "==", Value: "cmd.exe"}},
				},
			}},
			Until: &SequenceStepInfo{
				EventCategory:  "file",
				Missing:        true,
				ConditionCount: 1,
				Subquery: &ParseResult{
					Conditions: []Condition{{Field: "file.name", Operator: "==", Value: "stop.txt"}},
				},
			},
		},
	})
	if semantic == nil || semantic.Sequence == nil || len(semantic.Sequence.Steps) != 1 {
		t.Fatalf("semantic sequence = %#v, want one step", semantic)
	}
	step := semantic.Sequence.Steps[0]
	if step.Runs != 2 || step.Subquery == nil || step.Subquery.Expression == nil {
		t.Fatalf("semantic sequence step = %#v", step)
	}
	if got := step.Subquery.Expression.Condition.Field; got != "process.name" {
		t.Fatalf("step subquery field = %q, want process.name", got)
	}
	if semantic.Sequence.Until == nil || !semantic.Sequence.Until.Missing || semantic.Sequence.Until.Subquery == nil {
		t.Fatalf("semantic until step = %#v", semantic.Sequence.Until)
	}
}

func TestSemanticExpressionConversionEdges(t *testing.T) {
	if got := semanticFromExtraction(nil); got != nil {
		t.Fatalf("nil parse result converted to %#v", got)
	}
	if got := semanticSequenceStepsFromParseSteps(nil); got != nil {
		t.Fatalf("nil sequence steps converted to %#v", got)
	}
	if got := semanticSequenceStepFromParseStep(nil); got != nil {
		t.Fatalf("nil sequence step converted to %#v", got)
	}

	conditions := []Condition{
		{Field: "A", Operator: "==", Value: "1"},
		{Field: "B", Operator: "==", Value: "2"},
	}
	for name, expression := range map[string]*ConditionExpression{
		"bad_index": {
			Operator:       BooleanCondition,
			ConditionIndex: len(conditions),
		},
		"empty_boolean": {
			Operator: BooleanAnd,
		},
		"bad_child": {
			Operator: BooleanOr,
			Children: []*ConditionExpression{
				{Operator: BooleanCondition, ConditionIndex: 0},
				{Operator: BooleanCondition, ConditionIndex: len(conditions)},
			},
		},
		"unknown_operator": {
			Operator: "xor",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := semanticExpressionFromParseExpression(expression, conditions); got != nil {
				t.Fatalf("invalid expression converted to %#v", got)
			}
		})
	}

	leafA := &SemanticExpression{Operator: BooleanCondition, Condition: &conditions[0]}
	leafB := &SemanticExpression{Operator: BooleanCondition, Condition: &conditions[1]}
	if got := combineSemanticExpressions(BooleanAnd, nil, leafA); got != leafA {
		t.Fatalf("nil left combine = %#v, want leafA", got)
	}
	if got := combineSemanticExpressions(BooleanAnd, leafA, nil); got != leafA {
		t.Fatalf("nil right combine = %#v, want leafA", got)
	}
	combined := combineSemanticExpressions(
		BooleanAnd,
		&SemanticExpression{Operator: BooleanAnd, Children: []*SemanticExpression{leafA}},
		&SemanticExpression{Operator: BooleanAnd, Children: []*SemanticExpression{leafB}},
	)
	if combined.Operator != BooleanAnd || len(combined.Children) != 2 {
		t.Fatalf("flattened combine = %#v, want AND with two children", combined)
	}
}
