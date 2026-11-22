package eql

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The comment-free queries are hand-derived lexical oracles. Comparing the
// complete models catches predicate injection, literal changes and Boolean loss.
func TestNestedCommentsPublicWitnesses(t *testing.T) {
	for _, tc := range []struct {
		name, query, oracle string
		malformed           bool
	}{
		{"predicate injection", `process where a == 1 /* outer /* inner */ and b == 2 // */`, `process where a == 1`, false},
		{"basic", `process where a == 1 /* outer /* inner */ outer */ and c == 3`, `process where a == 1 and c == 3`, false},
		{"flat control", `process where a == 1 /* flat */ and c == 3`, `process where a == 1 and c == 3`, false},
		{"quoted control", `process where text == "/* outer /* inner */ tail */"`, `process where text == "/* outer /* inner */ tail */"`, false},
		{"unclosed outer", `process where a == 1 /* outer /* inner */`, `process where a == 1`, true},
		{"normalization interaction", `process where a == 1 /* outer /* inner */ " */ and b == "x–y"`, `process where a == 1 and b == "x–y"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assertNestedCommentModels(t, tc.query, tc.oracle, tc.malformed) })
	}
}

func assertNestedCommentModels(t *testing.T, query, oracle string, malformed bool) {
	t.Helper()
	ast, err := Parse(query)
	wantAST, wantErr := Parse(oracle)
	if wantErr != nil {
		t.Fatal(wantErr)
	}
	if malformed {
		if err == nil || err.Error() != "line 1:22: unterminated block comment" {
			t.Errorf("outer diagnostic: %v", err)
		}
	} else if err != nil {
		t.Errorf("Parse: %v", err)
	}
	if !reflect.DeepEqual(ast, wantAST) {
		t.Errorf("Parse model: %#v, want %#v", ast, wantAST)
	}
	r, s := ExtractConditions(query), AnalyzeQuery(query)
	if malformed {
		for name, errs := range map[string][]string{"extract": r.Errors, "analyze": s.Errors} {
			if len(errs) != 1 || errs[0] != "line 1:22: unterminated block comment" {
				t.Errorf("%s diagnostic: %v", name, errs)
			}
		}
		r.Errors = nil
		s.Errors = nil
	}
	if want := ExtractConditions(oracle); !reflect.DeepEqual(r, want) {
		t.Errorf("extracted model: %+v, want %+v", r, want)
	}
	if want := AnalyzeQuery(oracle); !reflect.DeepEqual(s, want) {
		t.Errorf("semantic model: %+v, want %+v", s, want)
	}
}

func TestNestedCommentsLexicalBoundaries(t *testing.T) {
	for _, comment := range []string{
		`/**//**/`, `/*/**/*/`, `/*/**/*//* x */`,
		`/* outer /* inner */ ' " ` + "`" + ` // \n */`,
		strings.Repeat("/*", 257) + ` " // ` + strings.Repeat("*/", 257),
	} {
		query := `process where a == 1 ` + comment + ` and (b == 2 or c == 3)`
		assertNestedCommentModels(t, query, `process where a == 1 and (b == 2 or c == 3)`, false)
		if got := NormalizeQuery(query); got != query {
			t.Errorf("normalization changed comment: %q", got)
		}
		expr, err := ParseExpression(`a == 1 ` + comment + ` and b == 2`)
		want, _ := ParseExpression(`a == 1 and b == 2`)
		if err != nil || !reflect.DeepEqual(expr, want) {
			t.Errorf("expression boundary: %#v / %v", expr, err)
		}
	}
	for _, literal := range []string{`"/* /* */ – */"`, `'/* /* */ – */'`, `?"/* /* */ – */"`, `"""/* /* */ – */"""`, "`/* /* */ – */`"} {
		query := `process where ` + literal + ` == "x–y"`
		if got := NormalizeQuery(query); got != query {
			t.Errorf("literal boundary: %q", got)
		}
		assertNestedCommentModels(t, query, query, false)
	}
	// Quotes and escaped whitespace inside the outer comment must not expose
	// the following string to typography or escaped-whitespace repairs.
	query := `process where a == 1 /* /* */ " \n */\nand b == "x–y\n"`
	want := `process where a == 1 /* /* */ " \n */` + "\n" + `and b == "x–y\n"`
	if got := NormalizeQuery(query); got != want {
		t.Errorf("escaped boundary: got %q want %q", got, want)
	}
	r, s := ExtractConditions(query), AnalyzeQuery(query)
	oracle := `process where a == 1 and b == "x–y\n"`
	if !reflect.DeepEqual(r, ExtractConditions(oracle)) || !reflect.DeepEqual(s, AnalyzeQuery(oracle)) {
		t.Errorf("escaped literal models: %+v / %+v", r, s)
	}
	for _, comment := range []string{`/*/`, `/* /* */`, `/*/**/*`} {
		_, err := Parse(`process where a == 1 ` + comment)
		if err == nil || !strings.Contains(err.Error(), "unterminated block comment") {
			t.Errorf("accepted unfinished outer %q: %v", comment, err)
		}
	}
}

func TestNestedCommentsSourcePositions(t *testing.T) {
	query := "process where a == 1 /* outer\n/* é */\n*/ and c == 3"
	lex := newLexer(query)
	if len(lex.errs) != 0 {
		t.Fatal(lex.errs)
	}
	found := false
	for _, tok := range lex.tokens {
		if tok.Value == "c" {
			found = true
			if tok.Pos != strings.Index(query, "c ==") || tok.Line != 3 || tok.Col != 8 {
				t.Errorf("original token position: %+v", tok)
			}
		}
	}
	if !found {
		t.Fatal("missing following field")
	}
	_, err := Parse("process where a == 1 /* outer\n/* inner */")
	if err == nil || err.Error() != "line 1:22: unterminated block comment" {
		t.Errorf("original opener: %v", err)
	}
}

func TestNestedCommentsWorkAndCancellation(t *testing.T) {
	query := `process where a == 1 /* outer /* inner */ ` + strings.Repeat("x", 512) + ` */ and b == 2`
	for _, analyze := range []bool{false, true} {
		budget := newParseBudget(context.Background())
		var errs []string
		if analyze {
			errs = analyzeWithBudget(query, budget).Errors
		} else {
			errs = extractWithBudget(query, budget).Errors
		}
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		used := maxParseWork - budget.work
		budget = newParseBudget(context.Background())
		budget.work = used - 1
		if analyze {
			r := analyzeWithBudget(query, budget)
			if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "(work)") || len(r.Conditions) != 0 || r.Expression != nil || len(r.Fields) != 0 {
				t.Fatalf("one-short analysis: %+v", r)
			}
		} else {
			r := extractWithBudget(query, budget)
			if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "(work)") || len(r.Conditions) != 0 || r.Expression != nil || len(r.Fields) != 0 {
				t.Fatalf("one-short extraction: %+v", r)
			}
		}
	}
	for _, api := range []string{"parse", "extract", "analyze"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		switch api {
		case "parse":
			r, err := ParseContext(ctx, query)
			if !errors.Is(err, context.Canceled) || r.Body != nil || len(r.Pipes) != 0 {
				t.Fatalf("canceled parse: %+v / %v", r, err)
			}
		case "extract":
			r := ExtractConditionsContext(ctx, query)
			if len(r.Errors) != 1 || r.Errors[0] != context.Canceled.Error() || len(r.Conditions) != 0 || r.Expression != nil || len(r.Fields) != 0 {
				t.Fatalf("canceled extraction: %+v", r)
			}
		case "analyze":
			r := AnalyzeQueryContext(ctx, query)
			if len(r.Errors) != 1 || r.Errors[0] != context.Canceled.Error() || len(r.Conditions) != 0 || r.Expression != nil || len(r.Fields) != 0 {
				t.Fatalf("canceled analysis: %+v", r)
			}
		}
	}
}

// Both scanners must abort at the first canceled checkpoint, with no further
// scan work. Normalization uses the caller's prepaid work allowance unchanged.
func TestNestedCommentsScannerCancellation(t *testing.T) {
	query := "/* /* */ " + strings.Repeat("x", 512) + " */"
	for _, phase := range []string{"lexer", "typography", "whitespace"} {
		t.Run(phase, func(t *testing.T) {
			ctx := &checkpointContext{Context: context.Background(), remaining: 20}
			budget := newParseBudget(ctx)
			func() {
				defer func() {
					value := recover()
					aborted, ok := value.(parseAbort)
					if !ok || !errors.Is(aborted.err, context.Canceled) {
						t.Fatalf("missing synchronous cancellation: %v", value)
					}
				}()
				switch phase {
				case "lexer":
					newLexerWithBudget(query, budget)
				case "typography":
					outsideStringLiterals(query, typographicReplacer.Replace, budget)
				case "whitespace":
					decodeEscapedWhitespace(query+`\n`, budget)
				}
			}()
			if ctx.remaining != 0 {
				t.Fatalf("continued after cancellation: %d", ctx.remaining)
			}
			if phase != "lexer" && budget.work != maxParseWork {
				t.Fatalf("normalization charged prepaid work twice: %d", budget.work)
			}
		})
	}
	ctx := &checkpointContext{Context: context.Background(), remaining: 20}
	r := extractWithBudget("process where a == 1 "+query, newParseBudget(ctx))
	if ctx.remaining != 0 || len(r.Errors) != 1 || r.Errors[0] != context.Canceled.Error() || len(r.Conditions) != 0 || r.Expression != nil || len(r.Fields) != 0 {
		t.Fatalf("in-flight extraction exposed model or continued: %+v / %d", r, ctx.remaining)
	}
}
