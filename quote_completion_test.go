package eql

import "testing"

func TestQuoteStringEscapesNewline(t *testing.T) {
	rendered := ExprString(&Literal{Kind: LitString, Value: "line1\nline2"})
	if rendered != `"line1\nline2"` {
		t.Fatalf("newline string rendered as %q", rendered)
	}
}

func TestNormalizeQueryDecodesEscapedTabOutsideStrings(t *testing.T) {
	query := NormalizeQuery(`process\twhere message == "literal\tkept"`)
	if query != "process\twhere message == \"literal\\tkept\"" {
		t.Fatalf("escaped tab normalization = %q", query)
	}
}
