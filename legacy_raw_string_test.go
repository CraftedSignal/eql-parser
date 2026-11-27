package eql

import (
	"strings"
	"testing"
)

// In a legacy raw string an escaped quote does not end the string (Endgame's
// RAW_DQ_STRING: /\?"(\\\"|[^"\r\n])*"/, and Elasticsearch's grammar), and it
// is the only escape Endgame's parser decodes: "only unescape the quote
// character".
func TestLegacyRawStringEscapedQuote(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`process where a == ?"x\"y" and b == 1`, `a == x"y`},
		{`process where a == ?'it\'s' and b == 1`, `a == it's`},
		{`process where a == ?"C:\x\\"" and b == 1`, `a == C:\x\"`},
	} {
		res := ExtractConditions(tc.query)
		requireNoErrors(t, res)
		requireConditions(t, res, tc.want, "b == 1")
	}
}

// With no later quote on its line, an escaped quote does end the string, so
// a raw path may still end in a backslash.
func TestLegacyRawStringTrailingBackslash(t *testing.T) {
	res := ExtractConditions(`process where a == ?"C:\Users\Public\"`)
	requireNoErrors(t, res)
	requireConditions(t, res, `a == C:\Users\Public\`)

	tokens := lex(`?"a\"b" ?'c\'`)
	if tokens[0].Type != TokenString || !tokens[0].Raw || tokens[0].Value != `a"b` {
		t.Errorf("token 0 = %+v, want raw string a\"b", tokens[0])
	}
	if tokens[1].Type != TokenString || !tokens[1].Raw || tokens[1].Value != `c\` {
		t.Errorf("token 1 = %+v, want raw string c\\", tokens[1])
	}
}

// A raw string is single-line, like every other EQL string, so an
// unterminated one does not swallow the next line.
func TestLegacyRawStringEndsAtLineBreak(t *testing.T) {
	res := ExtractConditions("process where a == ?\"x\nand b == \"y\"")
	requireConditions(t, res, "a == x", "b == y")
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "unterminated string") {
		t.Errorf("errors = %q, want one unterminated string", res.Errors)
	}
}

func TestLegacyRawStringTrailingEscapedQuoteBeforeLineBreak(t *testing.T) {
	for _, tc := range []struct{ name, lineEnding string }{
		{"LF", "\n"},
		{"CRLF", "\r\n"},
		{"CR", "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := "process where a == ?\"x\\\"" + tc.lineEnding + "and b == \"y\""
			res := ExtractConditions(query)
			requireNoErrors(t, res)
			requireConditions(t, res, "a == x\\", "b == y")
		})
	}
}
