package eql

import (
	"strings"
	"testing"
)

// A query taken from JSON without unescaping carries its line breaks as a
// literal \n. NormalizeQuery turns those into real line breaks outside string
// literals; a comment must end there instead of swallowing the rest.
func TestNormalizeEscapedNewlineEndsComment(t *testing.T) {
	for _, query := range []string{
		`process where a == 1 // note\nand b == 2`,
		`process where a == 1 // note\r\nand b == 2`,
		`process where a == 1 // note\rand b == 2`,
		`process where a == 1 /* user's */\nand b == 2`,
		`process where a == 1 /* see http://x */\nand b == 2`,
		`process where a == 1 // don't\nand b == 2`,
	} {
		res := ExtractConditions(query)
		if len(res.Errors) != 0 {
			t.Errorf("%s: errors %q", query, res.Errors)
		}
		requireConditions(t, res, "a == 1", "b == 2")
	}
}

// As in the lexer, an unterminated block comment runs to the end of the query.
func TestNormalizeUnterminatedBlockComment(t *testing.T) {
	res := ExtractConditions(`process where a == 1 /* it's\nand b == 2`)
	requireConditions(t, res, "a == 1")
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "unterminated block comment") {
		t.Errorf("errors = %q, want one unterminated block comment", res.Errors)
	}
}

// In a query with real line breaks a // comment still runs to the line break,
// even when its text holds a backslash-n, as a Windows path does.
func TestNormalizeLineCommentKeepsEscapeText(t *testing.T) {
	res := ExtractConditions("process where a == 1 // C:\\Windows\\notepad.exe\nand b == 2")
	requireNoErrors(t, res)
	requireConditions(t, res, "a == 1", "b == 2")
}

func TestNormalizeCROnlyCommentPreservesFollowingLiteral(t *testing.T) {
	query := "process where a == 1 // comment\rand b == \"x–y\""
	want := query
	if got := NormalizeQuery(query); got != want {
		t.Fatalf("NormalizeQuery() = %q, want %q", got, want)
	}
	res := ExtractConditions(query)
	requireNoErrors(t, res)
	requireConditions(t, res, "a == 1", "b == x–y")
}

// A quote inside a comment opens no string literal, so the literal after the
// comment keeps its typographic characters.
func TestNormalizeQuoteInsideCommentKeepsLiteral(t *testing.T) {
	for _, tc := range []struct{ query, field string }{
		{`process where /* 24" screen */ a == "x–y"`, "a"},
		{`process where a == 'it' /* it's */ and b == 'x–y'`, "b"},
		{`process where a == 1 // it's\nand b == "x–y"`, "b"},
		{"process where a == 1 // 24\" screen\nand b == \"x–y\"", "b"},
	} {
		if got := condValues(ExtractConditions(tc.query), tc.field); len(got) != 1 || got[0] != "x–y" {
			t.Errorf("%s: %s = %q, want [x–y]", tc.query, tc.field, got)
		}
		if NormalizeQuery(tc.query) == NormalizeQuery(strings.ReplaceAll(tc.query, "–", "-")) {
			t.Errorf("%s: en dash and hyphen normalise to the same text", tc.query)
		}
	}
}
