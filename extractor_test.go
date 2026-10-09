package eql

import (
	"strings"
	"testing"
)

// cond builds a compact "field op value" signature for assertions.
func condSig(c Condition) string {
	s := c.Field + " " + c.Operator + " " + c.Value
	if c.Negated {
		s = "not " + s
	}
	return s
}

func requireConditions(t *testing.T, res *ParseResult, want ...string) {
	t.Helper()
	var got []string
	for _, c := range res.Conditions {
		got = append(got, condSig(c))
	}
	if len(got) != len(want) {
		t.Fatalf("condition count = %d, want %d\ngot:  %v\nwant: %v\nerrors: %v",
			len(got), len(want), got, want, res.Errors)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("condition[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func requireNoErrors(t *testing.T, res *ParseResult) {
	t.Helper()
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
}

// ---------------------------------------------------------------------------
// Basic condition extraction
// ---------------------------------------------------------------------------

func TestExtractBasicEquality(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe"`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)

	c := res.Conditions[0]
	if c.EventCategory != "process" {
		t.Errorf("EventCategory = %q, want process", c.EventCategory)
	}
	if c.SequenceStep != 0 || c.PipeStage != 0 {
		t.Errorf("step/pipe = %d/%d, want 0/0", c.SequenceStep, c.PipeStage)
	}
	if c.CaseInsensitive {
		t.Error("== must be case-sensitive")
	}
	if len(res.EventCategories) != 1 || res.EventCategories[0] != "process" {
		t.Errorf("EventCategories = %v", res.EventCategories)
	}
	if len(res.Fields) != 1 || res.Fields[0] != "process.name" {
		t.Errorf("Fields = %v", res.Fields)
	}
}

func TestExtractSeqOperatorIsCaseInsensitive(t *testing.T) {
	res := ExtractConditions(`process where process.name : "CMD.EXE"`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name : CMD.EXE`)
	if !res.Conditions[0].CaseInsensitive {
		t.Error(": operator must be marked case-insensitive")
	}
}

func TestExtractComparisonOperators(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{`network where destination.port > 1024`, `destination.port > 1024`},
		{`network where destination.port >= 1024`, `destination.port >= 1024`},
		{`network where destination.port < 1024`, `destination.port < 1024`},
		{`network where destination.port <= 1024`, `destination.port <= 1024`},
		{`process where process.pid != 4`, `process.pid != 4`},
		{`process where process.interactive == true`, `process.interactive == true`},
		{`process where process.env == null`, `process.env == null`},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		requireNoErrors(t, res)
		requireConditions(t, res, tt.want)
	}
}

func TestExtractReversedComparison(t *testing.T) {
	res := ExtractConditions(`network where 1024 < destination.port`)
	requireNoErrors(t, res)
	requireConditions(t, res, `destination.port > 1024`)
}

func TestExtractFieldToFieldComparison(t *testing.T) {
	res := ExtractConditions(`process where process.name == process.parent.name`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == process.parent.name`)
	if !res.Conditions[0].ValueIsField {
		t.Error("ValueIsField should be set")
	}
	if len(res.Fields) != 2 {
		t.Errorf("Fields = %v, want both sides", res.Fields)
	}
}

func TestExtractLogicalOps(t *testing.T) {
	res := ExtractConditions(`process where process.name == "a.exe" and user.name == "root" or process.pid == 1`)
	requireNoErrors(t, res)
	requireConditions(t, res,
		`process.name == a.exe`,
		`user.name == root`,
		`process.pid == 1`,
	)
	if res.Conditions[0].LogicalOp != "" {
		t.Errorf("first LogicalOp = %q, want empty", res.Conditions[0].LogicalOp)
	}
	if res.Conditions[1].LogicalOp != "AND" {
		t.Errorf("second LogicalOp = %q, want AND", res.Conditions[1].LogicalOp)
	}
	if res.Conditions[2].LogicalOp != "OR" {
		t.Errorf("third LogicalOp = %q, want OR", res.Conditions[2].LogicalOp)
	}
}

func TestExtractNegation(t *testing.T) {
	res := ExtractConditions(`process where not process.name == "cmd.exe"`)
	requireNoErrors(t, res)
	requireConditions(t, res, `not process.name == cmd.exe`)
}

func TestExtractDoubleNegation(t *testing.T) {
	res := ExtractConditions(`process where not (not process.name == "cmd.exe")`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

func TestExtractDeMorgan(t *testing.T) {
	res := ExtractConditions(`process where not (process.name == "a" or user.name == "b")`)
	requireNoErrors(t, res)
	requireConditions(t, res,
		`not process.name == a`,
		`not user.name == b`,
	)
	// not (a or b) == not a AND not b
	if res.Conditions[1].LogicalOp != "AND" {
		t.Errorf("LogicalOp = %q, want AND (De Morgan)", res.Conditions[1].LogicalOp)
	}
}

// ---------------------------------------------------------------------------
// Lists and alternatives
// ---------------------------------------------------------------------------

func TestExtractInList(t *testing.T) {
	res := ExtractConditions(`process where process.name in ("cmd.exe", "powershell.exe", "pwsh.exe")`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1 (errors: %v)", len(res.Conditions), res.Errors)
	}
	c := res.Conditions[0]
	if c.Operator != "in" || c.Value != "cmd.exe" {
		t.Errorf("got %s %s %s", c.Field, c.Operator, c.Value)
	}
	if len(c.Alternatives) != 3 || c.Alternatives[2] != "pwsh.exe" {
		t.Errorf("Alternatives = %v", c.Alternatives)
	}
	if c.CaseInsensitive || c.Negated {
		t.Error("plain in must be case-sensitive and not negated")
	}
}

func TestExtractSameFieldOrMergesAlternatives(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe" or process.name == "powershell.exe"`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1 (errors: %v): %#v", len(res.Conditions), res.Errors, res.Conditions)
	}
	c := res.Conditions[0]
	if c.Field != "process.name" || c.Operator != "==" {
		t.Fatalf("condition = %#v, want process.name equality", c)
	}
	if len(c.Alternatives) != 2 || c.Alternatives[0] != "cmd.exe" || c.Alternatives[1] != "powershell.exe" {
		t.Fatalf("Alternatives = %v, want [cmd.exe powershell.exe]", c.Alternatives)
	}
}

func TestExtractOrAcrossAndBranchesDoesNotMergeAlternatives(t *testing.T) {
	res := ExtractConditions(`process where ((process.executable : "*\\cmd.exe" and process.command_line : "*whoami*") or (process.command_line : "*hostname*" and process.executable : "*\\powershell.exe"))`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 4 {
		t.Fatalf("conditions = %d, want 4 (errors: %v): %#v", len(res.Conditions), res.Errors, res.Conditions)
	}
	for i, c := range res.Conditions {
		if len(c.Alternatives) > 0 {
			t.Fatalf("condition[%d] unexpectedly merged alternatives: %#v", i, c)
		}
	}
	if res.Conditions[0].Field != "process.executable" ||
		res.Conditions[1].Field != "process.command_line" ||
		res.Conditions[2].Field != "process.command_line" ||
		res.Conditions[3].Field != "process.executable" {
		t.Fatalf("condition fields = %#v, want executable/command_line/command_line/executable", res.Conditions)
	}
	if res.Conditions[1].LogicalOp != "AND" || res.Conditions[2].LogicalOp != "OR" || res.Conditions[3].LogicalOp != "AND" {
		t.Fatalf("logical ops = [%q %q %q %q], want [\"\" \"AND\" \"OR\" \"AND\"]",
			res.Conditions[0].LogicalOp,
			res.Conditions[1].LogicalOp,
			res.Conditions[2].LogicalOp,
			res.Conditions[3].LogicalOp,
		)
	}
}

func TestExtractNotIn(t *testing.T) {
	res := ExtractConditions(`process where process.name not in ("a", "b")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.Negated || c.Operator != "in" {
		t.Errorf("not in: negated=%v op=%s", c.Negated, c.Operator)
	}
}

func TestExtractInInsensitive(t *testing.T) {
	res := ExtractConditions(`process where process.name in~ ("CMD.EXE", "pwsh.exe")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.CaseInsensitive {
		t.Error("in~ must be case-insensitive")
	}
}

func TestExtractNotInInsensitive(t *testing.T) {
	res := ExtractConditions(`process where process.name not in~ ("a")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.Negated || !c.CaseInsensitive {
		t.Errorf("not in~: negated=%v insensitive=%v", c.Negated, c.CaseInsensitive)
	}
}

func TestExtractLikeAndRegex(t *testing.T) {
	res := ExtractConditions(`file where file.path like ("C:\\Windows\\*", "C:\\Users\\*") and file.name regex~ """.*\.(exe|dll)"""`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2 (errors: %v)", len(res.Conditions), res.Errors)
	}
	like := res.Conditions[0]
	if like.Operator != "like" || like.CaseInsensitive {
		t.Errorf("like: op=%s insensitive=%v", like.Operator, like.CaseInsensitive)
	}
	if len(like.Alternatives) != 2 || like.Alternatives[0] != `C:\Windows\*` {
		t.Errorf("like alternatives = %v", like.Alternatives)
	}
	re := res.Conditions[1]
	if re.Operator != "regex" || !re.CaseInsensitive {
		t.Errorf("regex~: op=%s insensitive=%v", re.Operator, re.CaseInsensitive)
	}
	if re.Value != `.*\.(exe|dll)` {
		t.Errorf("raw string value = %q", re.Value)
	}
}

func TestExtractSeqList(t *testing.T) {
	res := ExtractConditions(`process where process.name : ("cmd.exe", "powershell.exe")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Operator != ":" || !c.CaseInsensitive || len(c.Alternatives) != 2 {
		t.Errorf("got %+v", c)
	}
}

func TestMergeSameFieldOr(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe" or process.name == "powershell.exe" or process.name == "pwsh.exe"`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1 merged (got %+v)", len(res.Conditions), res.Conditions)
	}
	c := res.Conditions[0]
	if len(c.Alternatives) != 3 {
		t.Errorf("Alternatives = %v", c.Alternatives)
	}
}

func TestNoMergeAcrossDifferentFields(t *testing.T) {
	res := ExtractConditions(`process where process.name == "a" or user.name == "b"`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2", len(res.Conditions))
	}
}

func TestNoMergeAcrossAnd(t *testing.T) {
	res := ExtractConditions(`process where process.name == "a" and process.name == "b"`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 2 {
		t.Fatalf("AND conditions must not merge: %+v", res.Conditions)
	}
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func TestExtractWildcardFunction(t *testing.T) {
	res := ExtractConditions(`process where wildcard(process.command_line, "*-enc*", "*-EncodedCommand*")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "process.command_line" || c.Operator != "wildcard" {
		t.Errorf("got %+v", c)
	}
	if len(c.Alternatives) != 2 || c.Value != "*-enc*" {
		t.Errorf("values = %q %v", c.Value, c.Alternatives)
	}
	if c.Function != "wildcard" {
		t.Errorf("Function = %q", c.Function)
	}
}

func TestExtractCidrMatch(t *testing.T) {
	res := ExtractConditions(`network where cidrMatch(destination.ip, "10.0.0.0/8", "172.16.0.0/12")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "destination.ip" || c.Operator != "cidrMatch" {
		t.Errorf("got %+v", c)
	}
}

func TestExtractTildeFunction(t *testing.T) {
	res := ExtractConditions(`process where endsWith~(process.name, ".exe")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Operator != "endsWith" || !c.CaseInsensitive {
		t.Errorf("got op=%s insensitive=%v", c.Operator, c.CaseInsensitive)
	}
}

func TestExtractFunctionInComparison(t *testing.T) {
	res := ExtractConditions(`process where length(process.args) >= 2`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "process.args" || c.Operator != ">=" || c.Value != "2" {
		t.Errorf("got %+v", c)
	}
	if c.Function != "length" {
		t.Errorf("Function = %q, want length", c.Function)
	}
}

func TestExtractNegatedFunction(t *testing.T) {
	res := ExtractConditions(`process where not startsWith(process.name, "win")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.Negated || c.Operator != "startsWith" {
		t.Errorf("got %+v", c)
	}
}

func TestExtractNestedFunction(t *testing.T) {
	res := ExtractConditions(`process where stringContains(concat(process.name, process.args), "x")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Field != "process.name" {
		t.Errorf("Field = %q, want first field in args", c.Field)
	}
}

// ---------------------------------------------------------------------------
// Fields: optional, backtick, array index
// ---------------------------------------------------------------------------

func TestExtractOptionalField(t *testing.T) {
	res := ExtractConditions(`process where ?process.env.SESSIONNAME != null`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if !c.IsOptional || c.Field != "process.env.SESSIONNAME" {
		t.Errorf("got %+v", c)
	}
}

func TestExtractBacktickField(t *testing.T) {
	res := ExtractConditions("process where `weird-field.name` == \"x\"")
	requireNoErrors(t, res)
	if res.Conditions[0].Field != "weird-field.name" {
		t.Errorf("Field = %q", res.Conditions[0].Field)
	}
}

func TestExtractArrayIndexField(t *testing.T) {
	res := ExtractConditions(`process where process.args[0] == "--inject"`)
	requireNoErrors(t, res)
	if res.Conditions[0].Field != "process.args[0]" {
		t.Errorf("Field = %q", res.Conditions[0].Field)
	}
}

func TestExtractBareBooleanField(t *testing.T) {
	res := ExtractConditions(`process where process.interactive`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.interactive == true`)
}

// ---------------------------------------------------------------------------
// Sequences, joins, samples
// ---------------------------------------------------------------------------

func TestExtractSequenceFull(t *testing.T) {
	q := `sequence by host.id with maxspan=30s
	  [process where process.name == "cmd.exe"] by process.pid
	  [network where destination.port == 445] by process.pid
	until [process where event.type == "end"]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)

	if res.Sequence == nil {
		t.Fatal("Sequence is nil")
	}
	s := res.Sequence
	if s.Kind != "sequence" || s.MaxSpan != "30s" || s.MaxSpanMS != 30000 {
		t.Errorf("sequence info = %+v", s)
	}
	if len(s.ByFields) != 1 || s.ByFields[0] != "host.id" {
		t.Errorf("global by = %v", s.ByFields)
	}
	if len(s.Steps) != 2 {
		t.Fatalf("steps = %d", len(s.Steps))
	}
	if s.Steps[0].EventCategory != "process" || s.Steps[1].EventCategory != "network" {
		t.Errorf("step categories = %+v", s.Steps)
	}
	if len(s.Steps[0].ByFields) != 1 || s.Steps[0].ByFields[0] != "process.pid" {
		t.Errorf("step by = %v", s.Steps[0].ByFields)
	}
	if s.Until == nil || s.Until.EventCategory != "process" {
		t.Errorf("until = %+v", s.Until)
	}

	requireConditions(t, res,
		`process.name == cmd.exe`,
		`destination.port == 445`,
		`event.type == end`,
	)
	if res.Conditions[0].SequenceStep != 0 || res.Conditions[1].SequenceStep != 1 {
		t.Errorf("steps = %d/%d", res.Conditions[0].SequenceStep, res.Conditions[1].SequenceStep)
	}
	if !res.Conditions[2].FromUntil {
		t.Error("until condition should be marked FromUntil")
	}
	wantKeys := []string{"host.id", "process.pid"}
	if strings.Join(res.JoinKeys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("JoinKeys = %v, want %v", res.JoinKeys, wantKeys)
	}
	if len(res.Commands) != 1 || res.Commands[0] != "sequence" {
		t.Errorf("Commands = %v", res.Commands)
	}
}

func TestExtractSequenceWithMaxspanBeforeBy(t *testing.T) {
	q := `sequence with maxspan=1h by user.name [authentication where event.outcome == "failure"] with runs=5 [authentication where event.outcome == "success"]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)
	s := res.Sequence
	if s == nil || s.MaxSpan != "1h" || len(s.ByFields) != 1 {
		t.Fatalf("sequence = %+v", s)
	}
	if s.Steps[0].Runs != 5 {
		t.Errorf("runs = %d, want 5", s.Steps[0].Runs)
	}
	if s.MaxSpanMS != 3600000 {
		t.Errorf("MaxSpanMS = %d", s.MaxSpanMS)
	}
}

func TestExtractSequenceMissingEvents(t *testing.T) {
	q := `sequence with maxspan=1m
	  [process where process.name == "sshd"]
	  ![process where event.type == "end"]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)
	s := res.Sequence
	if len(s.Steps) != 2 || !s.Steps[1].Missing {
		t.Fatalf("steps = %+v", s.Steps)
	}
	requireConditions(t, res,
		`process.name == sshd`,
		`event.type == end`,
	)
	if !res.Conditions[1].FromMissing {
		t.Error("condition from ![...] must be FromMissing")
	}
}

func TestExtractJoin(t *testing.T) {
	q := `join by user.name [process where true] [network where true]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)
	if res.Sequence == nil || res.Sequence.Kind != "join" {
		t.Fatalf("Sequence = %+v", res.Sequence)
	}
	if len(res.JoinKeys) != 1 || res.JoinKeys[0] != "user.name" {
		t.Errorf("JoinKeys = %v", res.JoinKeys)
	}
}

func TestExtractSample(t *testing.T) {
	q := `sample by host.id [any where uptime > 0] [any where port > 100] [any where bool_field == true]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)
	if res.Sequence == nil || res.Sequence.Kind != "sample" {
		t.Fatalf("Sequence = %+v", res.Sequence)
	}
	if len(res.Sequence.Steps) != 3 {
		t.Errorf("steps = %d", len(res.Sequence.Steps))
	}
	if len(res.EventCategories) != 1 || res.EventCategories[0] != "any" {
		t.Errorf("categories = %v", res.EventCategories)
	}
}

// ---------------------------------------------------------------------------
// Pipes
// ---------------------------------------------------------------------------

func TestExtractPipes(t *testing.T) {
	res := ExtractConditions(`process where process.name == "regsvr32.exe" | head 10 | tail 5`)
	requireNoErrors(t, res)
	if len(res.Pipes) != 2 || res.Pipes[0].Name != "head" || res.Pipes[1].Name != "tail" {
		t.Errorf("Pipes = %+v", res.Pipes)
	}
	if strings.Join(res.Commands, ",") != "head,tail" {
		t.Errorf("Commands = %v", res.Commands)
	}
	if res.Pipes[0].Args[0] != "10" {
		t.Errorf("head arg = %v", res.Pipes[0].Args)
	}
}

func TestExtractLegacyPipes(t *testing.T) {
	res := ExtractConditions(`process where true | unique process.name, user.name | count`)
	requireNoErrors(t, res)
	if len(res.Pipes) != 2 {
		t.Fatalf("Pipes = %+v", res.Pipes)
	}
	if len(res.Pipes[0].Args) != 2 {
		t.Errorf("unique args = %v", res.Pipes[0].Args)
	}
	// unique's fields land in Fields
	found := 0
	for _, f := range res.Fields {
		if f == "process.name" || f == "user.name" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("Fields = %v", res.Fields)
	}
}

func TestExtractFilterPipeConditions(t *testing.T) {
	res := ExtractConditions(`process where process.parent.name == "services.exe" | filter process.name : "cmd.exe"`)
	requireNoErrors(t, res)
	requireConditions(t, res,
		`process.parent.name == services.exe`,
		`process.name : cmd.exe`,
	)
	if res.Conditions[1].PipeStage != 1 {
		t.Errorf("filter condition PipeStage = %d, want 1", res.Conditions[1].PipeStage)
	}
}

// ---------------------------------------------------------------------------
// Bare expressions, categories, legacy syntax
// ---------------------------------------------------------------------------

func TestExtractBareExpression(t *testing.T) {
	res := ExtractConditions(`process.name : "mshta.exe" and process.parent.name : "winword.exe"`)
	requireNoErrors(t, res)
	requireConditions(t, res,
		`process.name : mshta.exe`,
		`process.parent.name : winword.exe`,
	)
	if len(res.EventCategories) != 0 {
		t.Errorf("bare expression should have no category: %v", res.EventCategories)
	}
}

func TestExtractQuotedCategory(t *testing.T) {
	res := ExtractConditions(`"my-custom-category" where a.b == 1`)
	requireNoErrors(t, res)
	if res.EventCategories[0] != "my-custom-category" {
		t.Errorf("categories = %v", res.EventCategories)
	}
}

func TestExtractAnyCategory(t *testing.T) {
	res := ExtractConditions(`any where network.direction == "outbound"`)
	requireNoErrors(t, res)
	if res.EventCategories[0] != "any" {
		t.Errorf("categories = %v", res.EventCategories)
	}
}

func TestExtractAnyWhereTrue(t *testing.T) {
	res := ExtractConditions(`any where true`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 0 {
		t.Errorf("conditions = %+v", res.Conditions)
	}
}

func TestExtractLegacySingleQuotes(t *testing.T) {
	res := ExtractConditions(`process where process.name == 'cmd.exe'`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

func TestExtractLegacyAssignEquality(t *testing.T) {
	res := ExtractConditions(`process where process.name = "cmd.exe"`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

func TestExtractLineage(t *testing.T) {
	res := ExtractConditions(`process where child of [process where process.name == "services.exe"]`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == services.exe`)
	c := res.Conditions[0]
	if c.Lineage != "child" {
		t.Errorf("Lineage = %q", c.Lineage)
	}
	if c.EventCategory != "process" {
		t.Errorf("EventCategory = %q", c.EventCategory)
	}
}

func TestExtractLineageCombined(t *testing.T) {
	res := ExtractConditions(`process where process.name == "net.exe" and descendant of [process where process.name == "cmd.exe"]`)
	requireNoErrors(t, res)
	requireConditions(t, res,
		`process.name == net.exe`,
		`process.name == cmd.exe`,
	)
	if res.Conditions[1].Lineage != "descendant" {
		t.Errorf("Lineage = %q", res.Conditions[1].Lineage)
	}
	if res.Conditions[1].LogicalOp != "AND" {
		t.Errorf("LogicalOp = %q", res.Conditions[1].LogicalOp)
	}
}

// ---------------------------------------------------------------------------
// String forms and escapes
// ---------------------------------------------------------------------------

func TestExtractEscapes(t *testing.T) {
	res := ExtractConditions(`file where file.path == "C:\\Windows\\System32\\cmd.exe"`)
	requireNoErrors(t, res)
	if res.Conditions[0].Value != `C:\Windows\System32\cmd.exe` {
		t.Errorf("Value = %q", res.Conditions[0].Value)
	}
}

func TestExtractUnicodeEscape(t *testing.T) {
	res := ExtractConditions(`file where file.name : "invoice\u{202e}fdp.exe"`)
	requireNoErrors(t, res)
	if res.Conditions[0].Value != "invoice‮fdp.exe" {
		t.Errorf("Value = %q", res.Conditions[0].Value)
	}
}

func TestExtractTripleQuoted(t *testing.T) {
	res := ExtractConditions(`process where match(process.command_line, """.*\\admin\$.*""")`)
	requireNoErrors(t, res)
	c := res.Conditions[0]
	if c.Value != `.*\\admin\$.*` {
		t.Errorf("raw value = %q", c.Value)
	}
	if c.Operator != "match" {
		t.Errorf("op = %q", c.Operator)
	}
}

// ---------------------------------------------------------------------------
// Normalization
// ---------------------------------------------------------------------------

func TestNormalizeSmartQuotes(t *testing.T) {
	res := ExtractConditions("process where process.name == “cmd.exe”")
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

// Typographic characters are fixed only outside string literals: an en dash
// inside a value is a different character to match (a Windows flag variant).
func TestNormalizeKeepsTypographicCharactersInsideStrings(t *testing.T) {
	query := "process where process.command_line : \"* \u2013enc *\" and process.name == \u201ccmd.exe\u201d"
	if got := NormalizeQuery(query); got != "process where process.command_line : \"* \u2013enc *\" and process.name == \"cmd.exe\"" {
		t.Fatalf("NormalizeQuery = %q", got)
	}
}

func TestNormalizeCodeFence(t *testing.T) {
	res := ExtractConditions("```eql\nprocess where process.name == \"cmd.exe\"\n```")
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

func TestNormalizeEscapedNewlines(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe"\nand user.name == "root"`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 2 {
		t.Errorf("conditions = %+v (errors %v)", res.Conditions, res.Errors)
	}
}

func TestNormalizeTrailingSemicolon(t *testing.T) {
	res := ExtractConditions(`process where process.name == "cmd.exe";`)
	requireNoErrors(t, res)
	requireConditions(t, res, `process.name == cmd.exe`)
}

// ---------------------------------------------------------------------------
// Robustness
// ---------------------------------------------------------------------------

func TestExtractEmptyQuery(t *testing.T) {
	for _, q := range []string{"", "   ", "\n\t", "// only a comment"} {
		res := ExtractConditions(q)
		if res == nil || res.Conditions == nil {
			t.Fatalf("nil result for %q", q)
		}
		if len(res.Errors) == 0 {
			t.Errorf("expected error for %q", q)
		}
	}
}

func TestExtractGarbage(t *testing.T) {
	for _, q := range []string{
		"@@@@@@",
		"process where ((((((",
		"sequence [",
		`process where process.name == `,
		"]] where [[",
		"process where a == \"unterminated",
	} {
		res := ExtractConditions(q)
		if res == nil || res.Conditions == nil {
			t.Fatalf("nil result for %q", q)
		}
		if len(res.Errors) == 0 {
			t.Errorf("expected errors for %q", q)
		}
	}
}

func TestExtractOversizedInput(t *testing.T) {
	huge := strings.Repeat("a", MaxInputSize+1)
	res := ExtractConditions(huge)
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "maximum size") {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestExtractDeepNesting(t *testing.T) {
	q := "process where " + strings.Repeat("(", 5000) + `a == "1"` + strings.Repeat(")", 5000)
	res := ExtractConditions(q)
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Errors) == 0 {
		t.Error("expected nesting error")
	}
}

func TestConditionsNeverEmptyFieldOrOperator(t *testing.T) {
	queries := []string{
		`process where process.name == "a" and (b == 1 or not c : "x")`,
		`sequence [a where x == 1] [b where y == 2]`,
		`any where true | filter z == "q"`,
	}
	for _, q := range queries {
		res := ExtractConditions(q)
		for i, c := range res.Conditions {
			if c.Field == "" || c.Operator == "" {
				t.Errorf("query %q condition %d has empty field/operator: %+v", q, i, c)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Field usage classification
// ---------------------------------------------------------------------------

func TestClassifyFieldUsage(t *testing.T) {
	q := `sequence by host.id [process where process.name == "a"] [network where destination.port == 445] until [process where process.name == "b"]`
	res := ExtractConditions(q)
	requireNoErrors(t, res)

	host := ClassifyFieldUsage(res, "host.id")
	if !host.IsJoinKey {
		t.Error("host.id should be a join key")
	}
	name := ClassifyFieldUsage(res, "process.name")
	if len(name.Steps) != 1 || name.Steps[0] != 0 {
		t.Errorf("process.name steps = %v", name.Steps)
	}
	if !name.InUntil {
		t.Error("process.name appears in until")
	}
	port := ClassifyFieldUsage(res, "destination.port")
	if len(port.Steps) != 1 || port.Steps[0] != 1 {
		t.Errorf("destination.port steps = %v", port.Steps)
	}
}

// ---------------------------------------------------------------------------
// Parser validation notes
// ---------------------------------------------------------------------------

func TestSequenceValidationErrors(t *testing.T) {
	tests := []struct {
		query   string
		wantErr string
	}{
		{`sequence [process where true]`, "at least two"},
		{`sequence with maxspan=1s [a where true] ![b where true] until [c where true]`, ""}, // valid
		{`sequence [a where true] ![b where true]`, "maxspan"},                               // missing events need maxspan
		{`join [a where true]`, "at least two"},
		{`sample [a where true] [b where true]`, "join keys"},
		{`sequence by a.b [x where true] [y where true] by c.d`, "join key count"},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		joined := strings.Join(res.Errors, "; ")
		if tt.wantErr == "" {
			if len(res.Errors) > 0 {
				t.Errorf("%q: unexpected errors %v", tt.query, res.Errors)
			}
			continue
		}
		if !strings.Contains(joined, tt.wantErr) {
			t.Errorf("%q: errors = %v, want substring %q", tt.query, res.Errors, tt.wantErr)
		}
	}
}

func TestPipeValidationErrors(t *testing.T) {
	tests := []struct {
		query   string
		wantErr string
	}{
		{`process where true | head`, "exactly one argument"},
		{`process where true | head 1.5`, "integer"},
		{`process where true | frobnicate 3`, "unknown pipe"},
		{`process where true | unique`, "at least one field"},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		joined := strings.Join(res.Errors, "; ")
		if !strings.Contains(joined, tt.wantErr) {
			t.Errorf("%q: errors = %v, want substring %q", tt.query, res.Errors, tt.wantErr)
		}
	}
}

func TestComparisonChainingError(t *testing.T) {
	res := ExtractConditions(`network where 100 < destination.port <= 200`)
	if !strings.Contains(strings.Join(res.Errors, ";"), "chaining") {
		t.Errorf("errors = %v", res.Errors)
	}
	// Extraction still yields both bounds.
	if len(res.Conditions) < 1 {
		t.Errorf("conditions = %+v", res.Conditions)
	}
}
