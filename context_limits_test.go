package eql

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExtractConditionsContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ExtractConditionsContext(ctx, `process where process.name == "editor"`)
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0], context.Canceled.Error()) {
		t.Fatalf("cancellation was not reported: %v", result.Errors)
	}
	if len(result.Conditions) != 0 || result.Expression != nil {
		t.Fatal("canceled extraction returned usable partial conditions")
	}
}

func TestParseContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseContext(ctx, `process where name == "editor"`); !errors.Is(err, context.Canceled) {
		t.Fatalf("ParseContext cancellation: %v", err)
	}
	if _, err := ParseExpressionContext(ctx, `name == "editor"`); !errors.Is(err, context.Canceled) {
		t.Fatalf("ParseExpressionContext cancellation: %v", err)
	}
}

func TestExtractionResourceBudgets(t *testing.T) {
	for _, resource := range []string{"work", "tokens", "nodes", "conditions", "output"} {
		t.Run(resource, func(t *testing.T) {
			budget := newParseBudget(context.Background())
			switch resource {
			case "work":
				budget.work = 8
			case "tokens":
				budget.tokens = 4
			case "nodes":
				budget.nodes = 4
			case "conditions":
				budget.conditions = 1
			case "output":
				budget.output = 128
			}
			result := extractWithBudget(`process where name == "editor" or path == "/usr/bin/editor"`, budget)
			if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], ErrResourceLimit.Error()+" ("+resource+")") {
				t.Fatalf("expected %s budget error, got %v", resource, result.Errors)
			}
			if len(result.Conditions) != 0 || result.Expression != nil {
				t.Fatal("resource failure returned partial conditions")
			}
		})
	}
}

func TestSequenceSubqueriesShareExtractionBudget(t *testing.T) {
	budget := newParseBudget(context.Background())
	budget.conditions = 3
	result := extractWithBudget(`sequence [process where name == "editor"] [file where path == "/tmp/note"]`, budget)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "(conditions)") || len(result.Conditions) != 0 {
		t.Fatalf("sequence subqueries reset the shared condition budget: %#v", result)
	}
}

// A context that becomes canceled after a few real lexer checkpoints makes
// in-flight cancellation deterministic without large inputs or sleeps.
type checkpointContext struct {
	context.Context
	remaining int
}

func (c *checkpointContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestCancellationStopsInFlightLexing(t *testing.T) {
	ctx := &checkpointContext{Context: context.Background(), remaining: 12}
	result := extractWithBudget(`process where process.name == "editor" and user.name == "reader"`, newParseBudget(ctx))
	if len(result.Errors) != 1 || result.Errors[0] != context.Canceled.Error() || len(result.Conditions) != 0 {
		t.Fatalf("in-flight cancellation: %#v", result)
	}
	if ctx.remaining != 0 {
		t.Fatalf("work continued after cancellation: %d checkpoints", -ctx.remaining)
	}
}
