package eql

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, query string) *Query {
	t.Helper()
	q, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", query, err)
	}
	return q
}

func TestParseEventQuery(t *testing.T) {
	q := mustParse(t, `process where process.name == "cmd.exe"`)
	e, ok := q.Body.(*EventQuery)
	if !ok {
		t.Fatalf("body = %T", q.Body)
	}
	if e.Category != "process" || e.Bare || e.CategoryAny {
		t.Errorf("event = %+v", e)
	}
	cmp, ok := e.Where.(*Binary)
	if !ok || cmp.Op != "==" {
		t.Fatalf("where = %T %v", e.Where, e.Where)
	}
	f, ok := cmp.L.(*Field)
	if !ok || f.Name() != "process.name" {
		t.Errorf("lhs = %v", cmp.L)
	}
	lit, ok := cmp.R.(*Literal)
	if !ok || lit.Value != "cmd.exe" {
		t.Errorf("rhs = %v", cmp.R)
	}
}

func TestParsePrecedence(t *testing.T) {
	// or binds loosest: (a and b) or c
	q := mustParse(t, `process where a == 1 and b == 2 or c == 3`)
	e := q.Body.(*EventQuery)
	or, ok := e.Where.(*Binary)
	if !ok || or.Op != "or" {
		t.Fatalf("root = %v", ExprString(e.Where))
	}
	and, ok := or.L.(*Binary)
	if !ok || and.Op != "and" {
		t.Errorf("or.L = %v", ExprString(or.L))
	}
}

func TestParseNotPrecedence(t *testing.T) {
	// not a == 1 and b == 2 → (not (a == 1)) and (b == 2)
	q := mustParse(t, `process where not a == 1 and b == 2`)
	e := q.Body.(*EventQuery)
	and, ok := e.Where.(*Binary)
	if !ok || and.Op != "and" {
		t.Fatalf("root = %v", ExprString(e.Where))
	}
	if _, ok := and.L.(*Not); !ok {
		t.Errorf("and.L = %T", and.L)
	}
}

func TestParseArithmeticPrecedence(t *testing.T) {
	// a + b * c == d → ((a + (b*c)) == d)
	q := mustParse(t, `process where a + b * c == 10`)
	e := q.Body.(*EventQuery)
	cmp := e.Where.(*Binary)
	if cmp.Op != "==" {
		t.Fatalf("root op = %s", cmp.Op)
	}
	add := cmp.L.(*Binary)
	if add.Op != "+" {
		t.Fatalf("lhs op = %s", add.Op)
	}
	mul := add.R.(*Binary)
	if mul.Op != "*" {
		t.Errorf("rhs op = %s", mul.Op)
	}
}

func TestParseUnaryMinus(t *testing.T) {
	q := mustParse(t, `process where exit_code == -1`)
	e := q.Body.(*EventQuery)
	cmp := e.Where.(*Binary)
	u, ok := cmp.R.(*Unary)
	if !ok || u.Op != "-" {
		t.Fatalf("rhs = %T", cmp.R)
	}
}

func TestParseSequenceHeaderOrders(t *testing.T) {
	q1 := mustParse(t, `sequence by host.id with maxspan=30s [a where true] [b where true]`)
	s1 := q1.Body.(*Sequence)
	if s1.MaxSpan != "30s" || len(s1.By) != 1 || s1.WithByOrder {
		t.Errorf("s1 = %+v", s1)
	}
	q2 := mustParse(t, `sequence with maxspan=30s by host.id [a where true] [b where true]`)
	s2 := q2.Body.(*Sequence)
	if s2.MaxSpan != "30s" || len(s2.By) != 1 || !s2.WithByOrder {
		t.Errorf("s2 = %+v", s2)
	}
}

func TestParseMaxspanUnits(t *testing.T) {
	for _, unit := range []string{"ms", "s", "m", "h", "d"} {
		q := mustParse(t, `sequence with maxspan=5`+unit+` [a where true] [b where true]`)
		s := q.Body.(*Sequence)
		if s.MaxSpan != "5"+unit {
			t.Errorf("maxspan = %q", s.MaxSpan)
		}
	}
	// Missing unit is an error but still parses.
	_, err := Parse(`sequence with maxspan=5 [a where true] [b where true]`)
	if err == nil || !strings.Contains(err.Error(), "time unit") {
		t.Errorf("err = %v", err)
	}
}

func TestParseSequenceStepModifiers(t *testing.T) {
	q := mustParse(t, `sequence [a where true] by x.y with runs=3 [b where true] by z.w`)
	s := q.Body.(*Sequence)
	if len(s.Steps) != 2 {
		t.Fatalf("steps = %d", len(s.Steps))
	}
	if s.Steps[0].Runs != 3 || s.Steps[0].WithKey != "runs" {
		t.Errorf("step0 = %+v", s.Steps[0])
	}
	if len(s.Steps[0].By) != 1 || len(s.Steps[1].By) != 1 {
		t.Errorf("per-step by missing")
	}
}

func TestParseRunsRange(t *testing.T) {
	_, err := Parse(`sequence [a where true] with runs=0 [b where true]`)
	if err == nil || !strings.Contains(err.Error(), "between 1 and 100") {
		t.Errorf("err = %v", err)
	}
	_, err = Parse(`sequence [a where true] with runs=101 [b where true]`)
	if err == nil {
		t.Error("runs=101 should error")
	}
}

func TestParseUntil(t *testing.T) {
	q := mustParse(t, `sequence [a where true] [b where true] until [c where true]`)
	s := q.Body.(*Sequence)
	if s.Until == nil || s.Until.Query.Category != "c" {
		t.Errorf("until = %+v", s.Until)
	}
}

func TestParseMissingEvent(t *testing.T) {
	q := mustParse(t, `sequence with maxspan=1m [a where true] ![b where true]`)
	s := q.Body.(*Sequence)
	if !s.Steps[1].Missing {
		t.Errorf("step1 = %+v", s.Steps[1])
	}
}

func TestParseSamples(t *testing.T) {
	q := mustParse(t, `sample by host.id [a where true] [b where true]`)
	s := q.Body.(*Sequence)
	if s.Kind != KindSample {
		t.Errorf("kind = %s", s.Kind)
	}
	// Sample rejects until/maxspan.
	_, err := Parse(`sample by k [a where true] [b where true] until [c where true]`)
	if err == nil || !strings.Contains(err.Error(), "until") {
		t.Errorf("err = %v", err)
	}
	_, err = Parse(`sample with maxspan=1s by k [a where true] [b where true]`)
	if err == nil || !strings.Contains(err.Error(), "maxspan") {
		t.Errorf("err = %v", err)
	}
}

func TestParseJoin(t *testing.T) {
	q := mustParse(t, `join by pid [a where true] [b where true] until [c where true]`)
	s := q.Body.(*Sequence)
	if s.Kind != KindJoin || s.Until == nil {
		t.Errorf("join = %+v", s)
	}
}

func TestParsePipesChained(t *testing.T) {
	q := mustParse(t, `process where true | unique process.name | sort process.name | head 5`)
	if len(q.Pipes) != 3 {
		t.Fatalf("pipes = %+v", q.Pipes)
	}
	if q.Pipes[2].Name != "head" {
		t.Errorf("pipes = %v %v %v", q.Pipes[0].Name, q.Pipes[1].Name, q.Pipes[2].Name)
	}
}

func TestParseInsensitiveVariants(t *testing.T) {
	q := mustParse(t, `process where a in~ ("x") and b like~ "y*" and c regex~ "z.*" and d : "w"`)
	e := q.Body.(*EventQuery)
	str := ExprString(e.Where)
	for _, want := range []string{"in~", "like~", "regex~", ": "} {
		if !strings.Contains(str, want) {
			t.Errorf("render %q missing %q", str, want)
		}
	}
}

func TestParseOptionalFieldInBy(t *testing.T) {
	q := mustParse(t, `sequence by ?user.id, host.name [a where true] [b where true]`)
	s := q.Body.(*Sequence)
	if len(s.By) != 2 {
		t.Fatalf("by = %+v", s.By)
	}
	f, ok := s.By[0].(*Field)
	if !ok || !f.Optional {
		t.Errorf("by[0] = %+v", s.By[0])
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		query   string
		wantErr string
	}{
		{``, "empty query"},
		{`process where`, "unexpected"},
		{`where true`, "missing event category"},
		{`process where a == `, "unexpected"},
		{`process where (a == 1`, "expected"},
		{`sequence`, "at least two"},
		{`process where a in (`, "empty list"},
		{`process where a : ()`, "empty pattern list"},
		{`process where f(g(h(`, "expected )"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.query)
		if err == nil {
			t.Errorf("Parse(%q): expected error containing %q", tt.query, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("Parse(%q) = %v, want substring %q", tt.query, err, tt.wantErr)
		}
	}
}

func TestParsePartialASTOnError(t *testing.T) {
	// Even with an error the AST should carry what parsed.
	q, err := Parse(`process where a == "1" and !!!`)
	if err == nil {
		t.Fatal("expected error")
	}
	if q == nil || q.Body == nil {
		t.Fatal("expected partial AST")
	}
}

func TestParseExpressionAPI(t *testing.T) {
	e, err := ParseExpression(`process.name : ("a", "b") and length(x) > 1`)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := e.(*Binary); !ok {
		t.Errorf("expr = %T", e)
	}
	if _, err := ParseExpression(""); err == nil {
		t.Error("empty expression should error")
	}
}

func TestParseLineageForms(t *testing.T) {
	for _, kind := range []string{"child", "descendant", "event"} {
		q := mustParse(t, `process where `+kind+` of [process where pid == 4]`)
		e := q.Body.(*EventQuery)
		lin, ok := e.Where.(*Lineage)
		if !ok || lin.Kind != kind {
			t.Errorf("%s: %+v", kind, e.Where)
		}
	}
}

func TestMaxSpanDuration(t *testing.T) {
	tests := []struct {
		span string
		ms   int64
		ok   bool
	}{
		{"30s", 30000, true},
		{"1m", 60000, true},
		{"2h", 7200000, true},
		{"1d", 86400000, true},
		{"500ms", 500, true},
		{"1.5h", 5400000, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		ms, ok := MaxSpanDuration(tt.span)
		if ok != tt.ok || ms != tt.ms {
			t.Errorf("MaxSpanDuration(%q) = %d,%v want %d,%v", tt.span, ms, ok, tt.ms, tt.ok)
		}
	}
}

// ---------------------------------------------------------------------------
// Round-trip property: parse → render → parse → render must be a fixpoint.
// ---------------------------------------------------------------------------

var roundTripQueries = []string{
	`process where process.name == "cmd.exe"`,
	`any where true`,
	`process where not (a == 1 or b == 2) and c : "x"`,
	`process where process.name in ("a", "b", "c")`,
	`process where process.name not in~ ("a", "b")`,
	`file where file.path like ("C:\\*", "D:\\*")`,
	`process where a.b.c regex~ """.*\.exe"""`,
	`network where cidrMatch(source.ip, "10.0.0.0/8")`,
	`process where endsWith~(process.name, ".exe") or length(process.args) >= 2`,
	`process where ?user.id != null`,
	`process where process.args[0] == "--inject" and process.args[1] : "*.dll"`,
	"process where `weird-field` == 1",
	`sequence by host.id with maxspan=30s [process where a == 1] by pid [network where b == 2] by pid until [process where c == 3]`,
	`sequence with maxspan=1m [a where true] ![b where x == "y"]`,
	`sequence [a where true] by x with runs=3 [b where true] by y`,
	`join by user.name [process where true] [network where true]`,
	`sample by host.id [a where true] [b where true]`,
	`process where true | head 10 | tail 5`,
	`process where true | unique process.name, user.name | count`,
	`process where p == 1 | filter q : "x" | sort r`,
	`process where child of [process where process.name == "services.exe"]`,
	`process where (a + b) * c % d == 10`,
	`process where a == -1 or b == +2.5 or c == 1e6`,
	`"quoted-category" where x == 1`,
	`process where descendant of [process where parent == "x"] and pid != 4`,
}

func TestRoundTrip(t *testing.T) {
	for _, query := range roundTripQueries {
		q1, err := Parse(query)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", query, err)
			continue
		}
		r1 := q1.String()
		q2, err := Parse(r1)
		if err != nil {
			t.Errorf("re-Parse(%q) [from %q] error: %v", r1, query, err)
			continue
		}
		r2 := q2.String()
		if r1 != r2 {
			t.Errorf("round-trip not stable:\n  orig:  %s\n  pass1: %s\n  pass2: %s", query, r1, r2)
		}
	}
}
