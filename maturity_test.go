package eql

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Recursive subquery model
// ---------------------------------------------------------------------------

func TestSequenceStepSubquery(t *testing.T) {
	q := `sequence by host.id with maxspan=5m
	  [process where process.name : "cmd.exe" and process.parent.name : "explorer.exe"]
	  [network where destination.port == 443]
	until [process where event.type == "end"]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)

	if res.Sequence == nil || len(res.Sequence.Steps) != 2 {
		t.Fatalf("sequence = %+v", res.Sequence)
	}

	// Step 0 subquery is self-contained: its own conditions, step-relative.
	sub0 := res.Sequence.Steps[0].Subquery
	if sub0 == nil {
		t.Fatal("step 0 has no subquery")
	}
	if len(sub0.Conditions) != 2 {
		t.Fatalf("step 0 subquery conditions = %+v", sub0.Conditions)
	}
	for _, c := range sub0.Conditions {
		if c.SequenceStep != 0 {
			t.Errorf("subquery condition should be step-relative (0), got %d", c.SequenceStep)
		}
		if c.EventCategory != "process" {
			t.Errorf("subquery category = %q", c.EventCategory)
		}
	}
	if len(sub0.EventCategories) != 1 || sub0.EventCategories[0] != "process" {
		t.Errorf("subquery categories = %v", sub0.EventCategories)
	}

	sub1 := res.Sequence.Steps[1].Subquery
	if sub1 == nil || len(sub1.Conditions) != 1 || sub1.Conditions[0].Field != "destination.port" {
		t.Errorf("step 1 subquery = %+v", sub1)
	}

	// Until clause also carries a subquery.
	if res.Sequence.Until == nil || res.Sequence.Until.Subquery == nil {
		t.Fatal("until has no subquery")
	}
	if res.Sequence.Until.Subquery.Conditions[0].Field != "event.type" {
		t.Errorf("until subquery = %+v", res.Sequence.Until.Subquery.Conditions)
	}
}

func TestSubqueryIndependentOfFlatView(t *testing.T) {
	// The flat top-level conditions carry sequence-step context; the per-step
	// subqueries are independent 0-based extractions of the same filters.
	q := `sequence [process where a == 1] [network where b == 2]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)

	// Flat view: step context preserved.
	if res.Conditions[0].SequenceStep != 0 || res.Conditions[1].SequenceStep != 1 {
		t.Errorf("flat steps = %d/%d", res.Conditions[0].SequenceStep, res.Conditions[1].SequenceStep)
	}
	// Structured view: each subquery is self-contained.
	if res.Sequence.Steps[1].Subquery.Conditions[0].SequenceStep != 0 {
		t.Error("subquery must be step-relative")
	}
}

// ---------------------------------------------------------------------------
// Co-fields in comparisons
// ---------------------------------------------------------------------------

func TestCoFieldsArithmetic(t *testing.T) {
	res := ExtractConditions(`network where source.bytes + destination.bytes > 1000000`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "source.bytes" {
		t.Fatalf("primary field = %q", c.Field)
	}
	if len(c.CoFields) != 1 || c.CoFields[0] != "destination.bytes" {
		t.Errorf("CoFields = %v, want [destination.bytes]", c.CoFields)
	}
}

func TestCoFieldsMultiple(t *testing.T) {
	res := ExtractConditions(`process where (a.x + b.y + c.z) % 3 == 0`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "a.x" {
		t.Fatalf("primary = %q", c.Field)
	}
	if strings.Join(c.CoFields, ",") != "b.y,c.z" {
		t.Errorf("CoFields = %v", c.CoFields)
	}
}

func TestNoCoFieldsForSimpleComparison(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe"`)
	requireNoErrors(t, res)
	if len(res.Conditions[0].CoFields) != 0 {
		t.Errorf("CoFields = %v, want none", res.Conditions[0].CoFields)
	}
}

func TestCoFieldsExcludeValueField(t *testing.T) {
	// Field-to-field comparison: the value field is not also a co-field.
	res := ExtractConditions(`process where process.name == process.parent.name`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.ValueIsField || len(c.CoFields) != 0 {
		t.Errorf("got ValueIsField=%v CoFields=%v", c.ValueIsField, c.CoFields)
	}
}

// ---------------------------------------------------------------------------
// Field provenance
// ---------------------------------------------------------------------------

func TestClassifyFieldProvenance(t *testing.T) {
	q := `sequence by host.id
	  [process where process.name : "a.exe"]
	  [network where destination.port == 445]
	until [process where user.name : "admin"]
	| unique event.category`
	res := ExtractConditions(q)
	requireNoErrors(t, res)

	tests := []struct {
		field string
		want  FieldProvenance
	}{
		{"host.id", ProvenanceJoinKey},         // by-clause key
		{"process.name", ProvenanceMain},       // first step = primary stream
		{"destination.port", ProvenanceJoined}, // later step = correlated event
		{"user.name", ProvenanceJoined},        // until clause = correlated event
		{"event.category", ProvenanceMain},     // referenced in a pipe = main stream
		{"nonexistent.field", ProvenanceUnknown},
	}
	for _, tt := range tests {
		got := ClassifyFieldProvenance(res, tt.field)
		if got != tt.want {
			t.Errorf("provenance(%q) = %q, want %q", tt.field, got, tt.want)
		}
	}
}

func TestClassifyFieldProvenanceAmbiguous(t *testing.T) {
	// A field used as both a join key and a filter is ambiguous.
	q := `sequence by process.name [process where process.name : "a.exe"] [network where true]`
	res := ExtractConditions(q)
	if got := ClassifyFieldProvenance(res, "process.name"); got != ProvenanceAmbiguous {
		t.Errorf("provenance = %q, want ambiguous", got)
	}
}

func TestClassifyFieldProvenanceNilResult(t *testing.T) {
	if got := ClassifyFieldProvenance(nil, "x"); got != ProvenanceUnknown {
		t.Errorf("provenance(nil) = %q", got)
	}
	res := ExtractConditions(`process where a == 1`)
	if got := ClassifyFieldProvenance(res, ""); got != ProvenanceUnknown {
		t.Errorf("provenance(empty field) = %q", got)
	}
}

func TestProvenanceCoField(t *testing.T) {
	// A co-field of a comparison counts as main provenance.
	res := ExtractConditions(`network where source.bytes + destination.bytes > 100`)
	if got := ClassifyFieldProvenance(res, "destination.bytes"); got != ProvenanceMain {
		t.Errorf("co-field provenance = %q, want main", got)
	}
}

// ---------------------------------------------------------------------------
// DeduplicateConditions
// ---------------------------------------------------------------------------

func TestDeduplicateConditions(t *testing.T) {
	conds := []Condition{
		{Field: "process.name", Operator: "==", Value: "cmd.exe"},
		{Field: "process.name", Operator: "==", Value: "cmd.exe"}, // exact dup
		{Field: "process.name", Operator: "==", Value: "cmd.exe", Negated: true},
		{Field: "process.name", Operator: "==", Value: "powershell.exe"},
		{Field: "process.name", Operator: "==", Value: "cmd.exe", SequenceStep: 1}, // different step
	}
	got := DeduplicateConditions(conds)
	if len(got) != 4 {
		t.Fatalf("dedup produced %d conditions, want 4: %+v", len(got), got)
	}
	// First occurrence order preserved.
	if got[0].Value != "cmd.exe" || got[0].Negated {
		t.Errorf("first = %+v", got[0])
	}
}

func TestDeduplicateConditionsEmpty(t *testing.T) {
	if got := DeduplicateConditions(nil); len(got) != 0 {
		t.Errorf("dedup(nil) = %+v", got)
	}
}

func TestDeduplicateDistinctContexts(t *testing.T) {
	// Same field/op/value but different pipe stage / until / category must not collapse.
	conds := []Condition{
		{Field: "f", Operator: "==", Value: "v", EventCategory: "process"},
		{Field: "f", Operator: "==", Value: "v", EventCategory: "network"},
		{Field: "f", Operator: "==", Value: "v", PipeStage: 1},
		{Field: "f", Operator: "==", Value: "v", FromUntil: true},
	}
	if got := DeduplicateConditions(conds); len(got) != 4 {
		t.Errorf("dedup collapsed distinct contexts: %d/4", len(got))
	}
}

func TestFieldInTextWholeToken(t *testing.T) {
	// Pipe-argument field matching must be whole-token, not substring.
	res := ExtractConditions(`process where a == 1 | unique process.name`)
	if got := ClassifyFieldProvenance(res, "process.na"); got != ProvenanceUnknown {
		t.Errorf("substring should not match: got %q", got)
	}
	if got := ClassifyFieldProvenance(res, "process.name"); got != ProvenanceMain {
		t.Errorf("whole token = %q, want main", got)
	}
}

// ---------------------------------------------------------------------------
// Helper family parity (IsStatisticalQuery, HasComplexWhereConditions, ...)
// ---------------------------------------------------------------------------

func TestIsStatisticalQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{`process where process.name : "cmd.exe"`, false},
		{`process where true | count`, true},
		{`process where true | unique process.name`, true},
		{`process where true | unique_count user.name`, true},
		{`process where true | head 10`, false},
		{`sequence by x [a where true] [b where true]`, true},
		{`sample by x [a where true] [b where true]`, true},
		{`join by x [a where true] [b where true]`, false}, // join is not statistical
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		if got := IsStatisticalQuery(res); got != tt.want {
			t.Errorf("IsStatisticalQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
	if IsStatisticalQuery(nil) {
		t.Error("IsStatisticalQuery(nil) should be false")
	}
}

func TestHasComplexWhereConditions(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{`process where process.pid == 4`, false},
		{`process where process.name : "cmd.exe"`, true},
		{`process where process.name like "cmd*"`, true},
		{`process where process.command_line regex "x.*"`, true},
		{`network where cidrMatch(source.ip, "10.0.0.0/8")`, true},
		{`process where process.name in ("a", "b")`, true},
		{`process where endsWith(process.name, ".exe")`, true},
		{`process where a == 1 and b != 2`, false},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		if got := HasComplexWhereConditions(res); got != tt.want {
			t.Errorf("HasComplexWhereConditions(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
	if HasComplexWhereConditions(nil) {
		t.Error("HasComplexWhereConditions(nil) should be false")
	}
}

func TestHasUnmappedComputedFields(t *testing.T) {
	// EQL has no computed fields; always false (API parity stub).
	if HasUnmappedComputedFields(ExtractConditions(`process where a == 1`)) {
		t.Error("EQL has no computed fields")
	}
	if HasUnmappedComputedFields(nil) {
		t.Error("nil should be false")
	}
}

func TestGetEventTypeFromConditions(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{`process where event.code == "4688"`, "windows_4688"},
		{`process where winlog.event_id == "4624"`, "windows_4624"},
		{`process where event.code : "1" and event.provider : "Microsoft-Windows-Sysmon"`, "sysmon_1"},
		{`process where event.code in ("4688", "4689")`, "windows_4688"},
		{`process where process.name : "cmd.exe"`, ""},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		if got := GetEventTypeFromConditions(res); got != tt.want {
			t.Errorf("GetEventTypeFromConditions(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
	if GetEventTypeFromConditions(nil) != "" {
		t.Error("nil should be empty")
	}
}

func TestGroupByFieldsAlias(t *testing.T) {
	res := ExtractConditions(`sequence by host.id, user.name [a where true] [b where true]`)
	gb := res.GroupByFields()
	if len(gb) != 2 || gb[0] != "host.id" || gb[1] != "user.name" {
		t.Errorf("GroupByFields = %v", gb)
	}
	var nilResult *ParseResult
	if nilResult.GroupByFields() != nil {
		t.Error("nil GroupByFields should be nil")
	}
}
