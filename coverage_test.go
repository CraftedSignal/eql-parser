package eql

import "testing"

// Targeted tests for real logic branches that the broad suites don't hit
// directly.

func TestReversedComparisonAllOps(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{`network where 1024 <= destination.port`, `destination.port >= 1024`},
		{`network where 1024 >= destination.port`, `destination.port <= 1024`},
		{`network where 1024 > destination.port`, `destination.port < 1024`},
		{`network where 1024 < destination.port`, `destination.port > 1024`},
		{`network where 443 == destination.port`, `destination.port == 443`},
		{`network where 443 != destination.port`, `destination.port != 443`},
	}
	for _, tt := range tests {
		res := ExtractConditions(tt.query)
		requireNoErrors(t, res)
		if len(res.Conditions) != 1 || condSig(res.Conditions[0]) != tt.want {
			t.Errorf("%q → %+v, want %q", tt.query, res.Conditions, tt.want)
		}
	}
}

func TestEventOfLineage(t *testing.T) {
	res := ExtractConditions(`process where event of [process where process.name == "svchost.exe"]`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 1 || res.Conditions[0].Lineage != "event" {
		t.Errorf("conditions = %+v", res.Conditions)
	}
}

func TestMaxSpanExoticUnits(t *testing.T) {
	tests := []struct {
		span string
		ms   int64
		ok   bool
	}{
		{"1000micros", 1, true},
		{"1000000nanos", 1, true},
		{"2days", 172800000, true},
		{"5minutes", 300000, true},
		{"3hours", 10800000, true},
		{"10", 10000, true}, // bare number defaults to seconds
		{"5weeks", 0, false},
	}
	for _, tt := range tests {
		ms, ok := MaxSpanDuration(tt.span)
		if ok != tt.ok || (ok && ms != tt.ms) {
			t.Errorf("MaxSpanDuration(%q) = %d,%v want %d,%v", tt.span, ms, ok, tt.ms, tt.ok)
		}
	}
}

func TestParseExpressionTrailingInput(t *testing.T) {
	_, err := ParseExpression(`a == 1 garbage trailing`)
	if err == nil {
		t.Error("expected trailing-input error")
	}
}

func TestExprStringNil(t *testing.T) {
	if ExprString(nil) != "" {
		t.Error("ExprString(nil) should be empty")
	}
}

func TestTokenTypeString(t *testing.T) {
	if TokenEQ.String() != "==" {
		t.Errorf("TokenEQ.String() = %q", TokenEQ.String())
	}
	// Out-of-range token type falls back to a synthetic name.
	if got := TokenType(9999).String(); got == "" {
		t.Error("unknown token type should have a name")
	}
}

func TestClassifyFieldUsageInPipes(t *testing.T) {
	res := ExtractConditions(`process where process.name : "cmd.exe" | unique user.name`)
	requireNoErrors(t, res)
	u := ClassifyFieldUsage(res, "user.name")
	if !u.InPipes {
		t.Errorf("user.name should be marked InPipes: %+v", u)
	}
}

func TestNotInIsNotLogicalNot(t *testing.T) {
	// `not in` must attach to the predicate, not read as logical `not (x in ...)`.
	res := ExtractConditions(`process where process.name not in ("a", "b")`)
	requireNoErrors(t, res)
	if len(res.Conditions) != 1 {
		t.Fatalf("conditions = %+v", res.Conditions)
	}
	c := res.Conditions[0]
	if !c.Negated || c.Operator != "in" || len(c.Alternatives) != 2 {
		t.Errorf("condition = %+v", c)
	}
}
