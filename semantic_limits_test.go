package eql

import (
	"context"
	"strings"
	"testing"
)

func TestAnalyzeQueryContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := AnalyzeQueryContext(ctx, `process where name == "editor"`)
	if len(result.Errors) != 1 || result.Errors[0] != context.Canceled.Error() {
		t.Fatalf("semantic cancellation was not reported: %#v", result)
	}
	if len(result.Conditions) != 0 || result.Expression != nil || result.Sequence != nil {
		t.Fatal("cancellation returned partial semantic output")
	}
}

func TestSemanticConversionSharesCallBudget(t *testing.T) {
	query := `process where name == "editor" or path == "/usr/bin/editor"`
	// Measure this small query's extraction allowance, then leave exactly
	// that allowance: semantic conversion must consume the same budget.
	measured := newParseBudget(context.Background())
	extracted := extractWithBudget(query, measured)
	if len(extracted.Errors) != 0 {
		t.Fatal(extracted.Errors)
	}
	budget := newParseBudget(context.Background())
	budget.nodes -= measured.nodes
	result := analyzeWithBudget(query, budget)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "(nodes)") {
		t.Fatalf("semantic conversion escaped the node budget: %#v", result)
	}
	if len(result.Conditions) != 0 || result.Expression != nil {
		t.Fatal("semantic resource failure returned partial output")
	}
}
