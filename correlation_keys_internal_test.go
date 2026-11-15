package eql

import (
	"context"
	"errors"
	"testing"
)

func TestCorrelationKeySemanticCloneOwnsDescriptors(t *testing.T) {
	parse := ExtractConditions("sequence by ?host.id, host.id [process where true] by ?process.pid [network where true] by network.pid until [process where true] by host.id")
	if len(parse.Errors) != 0 || parse.Sequence == nil || parse.Sequence.Until == nil {
		t.Fatalf("extraction failed: %#v", parse)
	}
	semantic := semanticFromExtraction(parse)
	if semantic == nil || semantic.Sequence == nil || semantic.Sequence.Until == nil {
		t.Fatalf("semantic clone failed: %#v", semantic)
	}
	if semantic.Sequence.ByKeys[0].Field == semantic.Sequence.ByKeys[1].Field {
		t.Fatal("duplicate authored keys share mutable field metadata")
	}
	semantic.Sequence.ByKeys[0].Syntax = "changed"
	semantic.Sequence.ByKeys[0].Field.Name = "changed"
	semantic.Sequence.ByKeys[0].Field.Optional = false
	semantic.Sequence.ByKeys[1] = CorrelationKey{Syntax: "replaced"}
	semantic.Sequence.Steps[0].ByKeys[0].Field.Name = "local-changed"
	semantic.Sequence.Until.ByKeys[0].Field.Name = "until-changed"
	if parse.Sequence.ByKeys[0].Syntax != "?host.id" || parse.Sequence.ByKeys[0].Field.Name != "host.id" || !parse.Sequence.ByKeys[0].Field.Optional || parse.Sequence.ByKeys[1].Syntax != "host.id" || parse.Sequence.Steps[0].ByKeys[0].Field.Name != "process.pid" || parse.Sequence.Until.ByKeys[0].Field.Name != "host.id" {
		t.Fatalf("parse descriptors aliased semantic clone: %#v", parse.Sequence)
	}
	if semantic.Sequence.ByKeys[0].Field == parse.Sequence.ByKeys[0].Field || semantic.Sequence.ByKeys[0].Field == semantic.Sequence.Steps[0].ByKeys[0].Field {
		t.Fatal("nested field pointers shared across views or scopes")
	}
}

func TestCorrelationKeyCloneBudgetAndCancellation(t *testing.T) {
	keys := []CorrelationKey{{Syntax: "?host.id", Field: &CorrelationKeyField{Name: "host.id", Optional: true}}, {Syntax: "host.id", Field: &CorrelationKeyField{Name: "host.id"}}}
	if cloneCorrelationKeysWithBudget(nil, nil) != nil || cloneCorrelationKeysWithBudget([]CorrelationKey{}, nil) != nil {
		t.Fatal("empty clone must be nil")
	}
	budget := newParseBudget(context.Background())
	budget.nodes = 1
	assertCorrelationKeyAbort(t, ErrResourceLimit, func() { cloneCorrelationKeysWithBudget(keys, budget) })
	budget = newParseBudget(context.Background())
	budget.output = 1
	assertCorrelationKeyAbort(t, ErrResourceLimit, func() { cloneCorrelationKeysWithBudget(keys, budget) })
	ctx, cancel := context.WithCancel(context.Background())
	budget = newParseBudget(ctx)
	cancel()
	assertCorrelationKeyAbort(t, context.Canceled, func() { cloneCorrelationKeysWithBudget(keys, budget) })
	field := &Field{Optional: true, Path: []PathSeg{{Name: "host"}, {Name: "id"}}}
	budget = newParseBudget(context.Background())
	budget.output = 1
	assertCorrelationKeyAbort(t, ErrResourceLimit, func() { correlationKeyFromExpr(field, budget) })
}

func assertCorrelationKeyAbort(t *testing.T, want error, run func()) {
	t.Helper()
	defer func() {
		value := recover()
		abort, ok := value.(parseAbort)
		if !ok || !errors.Is(abort.err, want) {
			t.Fatalf("got panic %v, want %v", value, want)
		}
	}()
	run()
	t.Fatalf("expected %v", want)
}
