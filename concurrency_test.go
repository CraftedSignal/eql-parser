package eql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Keep this bounded test active in short mode: the large corpus worker pools
// are skipped there. Distinct values expose cross-call result or budget reuse.
func TestConcurrentCallIsolation(t *testing.T) {
	const workers, iterations = 8, 24
	start := make(chan struct{})
	failures := make(chan error, workers)
	var ready, done sync.WaitGroup
	ready.Add(workers)
	done.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				if err := checkConcurrentCalls(worker, iteration); err != nil {
					failures <- fmt.Errorf("worker %d iteration %d: %w", worker, iteration, err)
					return
				}
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func checkConcurrentCalls(worker, iteration int) error {
	name := fmt.Sprintf("editor-%d-%d", worker, iteration)
	file := fmt.Sprintf("note-%d-%d", worker, iteration)
	ordinary := fmt.Sprintf(`process where process.name == %q or user.name == %q`, name, file)
	sequence := fmt.Sprintf(`sequence by host.id with maxspan=5m [process where process.name == %q] [file where file.name == %q]`, name, file)
	for _, query := range []string{ordinary, sequence} {
		// A canceled caller and exhausted budget must return no usable partial
		// output, without canceling or consuming another call's allowance.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if parsed, err := ParseContext(ctx, query); !errors.Is(err, context.Canceled) || parsed == nil || parsed.Body != nil {
			return fmt.Errorf("canceled parse: AST=%#v error=%v", parsed, err)
		}
		canceled := ExtractConditionsContext(ctx, query)
		if len(canceled.Errors) != 1 || canceled.Errors[0] != context.Canceled.Error() || len(canceled.Conditions) != 0 || canceled.Expression != nil || canceled.Sequence != nil {
			return fmt.Errorf("canceled extraction: %#v", canceled)
		}
		semantic := AnalyzeQueryContext(ctx, query)
		if len(semantic.Errors) != 1 || semantic.Errors[0] != context.Canceled.Error() || len(semantic.Conditions) != 0 || semantic.Expression != nil || semantic.Sequence != nil {
			return fmt.Errorf("canceled analysis: %#v", semantic)
		}
		budget := newParseBudget(context.Background())
		budget.conditions = 0
		limited := extractWithBudget(query, budget)
		if len(limited.Errors) != 1 || !strings.Contains(limited.Errors[0], ErrResourceLimit.Error()+" (conditions)") || len(limited.Conditions) != 0 || limited.Expression != nil || limited.Sequence != nil {
			return fmt.Errorf("limited extraction: %#v", limited)
		}

		parsed, err := Parse(query)
		if err != nil || parsed == nil || parsed.String() != query {
			return fmt.Errorf("valid parse: AST=%#v error=%v", parsed, err)
		}
		extracted := ExtractConditions(query)
		semantic = AnalyzeQuery(query)
		if len(extracted.Errors) != 0 || len(semantic.Errors) != 0 {
			return fmt.Errorf("valid calls inherited failure: extraction=%v analysis=%v", extracted.Errors, semantic.Errors)
		}
		want := []Condition{
			{Field: "process.name", Operator: "==", Value: name, EventCategory: "process", RHS: &Operand{Kind: OperandString, Text: name}, PredicateSyntax: fmt.Sprintf(`process.name == %q`, name)},
			{Field: "user.name", Operator: "==", Value: file, EventCategory: "process", LogicalOp: "OR", RHS: &Operand{Kind: OperandString, Text: file}, PredicateSyntax: fmt.Sprintf(`user.name == %q`, file)},
		}
		operator := BooleanOr
		wantExpression := &ConditionExpression{Operator: BooleanOr, Children: []*ConditionExpression{
			{Operator: BooleanCondition, ConditionIndex: 0},
			{Operator: BooleanCondition, ConditionIndex: 1},
		}}
		if query == sequence {
			want[1] = Condition{Field: "file.name", Operator: "==", Value: file, EventCategory: "file", SequenceStep: 1, RHS: &Operand{Kind: OperandString, Text: file}, PredicateSyntax: fmt.Sprintf(`file.name == %q`, file)}
			operator = BooleanAnd
			wantExpression = nil
			if err := checkConcurrentSequence(extracted.Sequence, semantic.Sequence, want); err != nil {
				return err
			}
		} else if extracted.Sequence != nil || semantic.Sequence != nil {
			return fmt.Errorf("ordinary query inherited sequence metadata")
		}
		if !reflect.DeepEqual(extracted.Conditions, want) || !reflect.DeepEqual(semantic.Conditions, want) {
			return fmt.Errorf("conditions: extraction=%#v analysis=%#v want=%#v", extracted.Conditions, semantic.Conditions, want)
		}
		if !reflect.DeepEqual(extracted.Expression, wantExpression) {
			return fmt.Errorf("extracted expression: %#v want=%#v", extracted.Expression, wantExpression)
		}
		wantSemantic := &SemanticExpression{Operator: operator, Children: []*SemanticExpression{
			{Operator: BooleanCondition, Condition: &want[0]},
			{Operator: BooleanCondition, Condition: &want[1]},
		}}
		if !reflect.DeepEqual(semantic.Expression, wantSemantic) {
			return fmt.Errorf("semantic expression: %#v want=%#v", semantic.Expression, wantSemantic)
		}
	}
	return nil
}

func checkConcurrentSequence(extracted *SequenceInfo, semantic *SemanticSequence, conditions []Condition) error {
	if extracted == nil || semantic == nil || extracted.Kind != "sequence" || semantic.Kind != "sequence" ||
		extracted.MaxSpan != "5m" || semantic.MaxSpan != "5m" || extracted.MaxSpanMS != 300000 || semantic.MaxSpanMS != 300000 ||
		!reflect.DeepEqual(extracted.ByFields, []string{"host.id"}) || !reflect.DeepEqual(semantic.ByFields, []string{"host.id"}) ||
		len(extracted.Steps) != 2 || len(semantic.Steps) != 2 {
		return fmt.Errorf("sequence metadata: extraction=%#v analysis=%#v", extracted, semantic)
	}
	for i, condition := range conditions {
		step, semanticStep := extracted.Steps[i], semantic.Steps[i]
		condition.SequenceStep = 0 // Step subqueries have their own indexes.
		if step.EventCategory != condition.EventCategory || semanticStep.EventCategory != condition.EventCategory || step.ConditionCount != 1 || semanticStep.ConditionCount != 1 || step.Subquery == nil || semanticStep.Subquery == nil {
			return fmt.Errorf("sequence step %d: extraction=%#v analysis=%#v", i, step, semanticStep)
		}
		want := []Condition{condition}
		wantExpression := &SemanticExpression{Operator: BooleanCondition, Condition: &condition}
		if len(step.Subquery.Errors) != 0 || len(semanticStep.Subquery.Errors) != 0 ||
			!reflect.DeepEqual(step.Subquery.Conditions, want) || !reflect.DeepEqual(semanticStep.Subquery.Conditions, want) ||
			!reflect.DeepEqual(semanticStep.Subquery.Expression, wantExpression) {
			return fmt.Errorf("sequence step %d lost its condition: extraction=%#v analysis=%#v", i, step.Subquery, semanticStep.Subquery)
		}
	}
	return nil
}
