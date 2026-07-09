package eql

import (
	"strings"
	"testing"
)

func lex(input string) []Token {
	return newLexer(input).tokens
}

func tokenTypes(toks []Token) []TokenType {
	var out []TokenType
	for _, t := range toks {
		out = append(out, t.Type)
	}
	return out
}

func TestLexBasicQuery(t *testing.T) {
	toks := lex(`process where process.name == "cmd.exe"`)
	want := []TokenType{TokenIdent, TokenWhere, TokenIdent, TokenDot, TokenIdent, TokenEQ, TokenString, TokenEOF}
	got := tokenTypes(toks)
	if len(got) != len(want) {
		t.Fatalf("tokens = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if toks[6].Value != "cmd.exe" {
		t.Errorf("string value = %q", toks[6].Value)
	}
}

func TestLexKeywordsCaseInsensitive(t *testing.T) {
	toks := lex(`PROCESS WHERE x AND y OR NOT z`)
	// PROCESS is an identifier (categories are not keywords), WHERE/AND/OR/NOT are keywords.
	if toks[1].Type != TokenWhere || toks[3].Type != TokenAnd || toks[5].Type != TokenOr || toks[6].Type != TokenNot {
		t.Errorf("tokens = %v", tokenTypes(toks))
	}
}

func TestLexAllOperators(t *testing.T) {
	tests := []struct {
		input string
		want  TokenType
	}{
		{"==", TokenEQ}, {"!=", TokenNEQ}, {"<", TokenLT}, {"<=", TokenLTE},
		{">", TokenGT}, {">=", TokenGTE}, {":", TokenColon}, {"=", TokenAssign},
		{"+", TokenPlus}, {"-", TokenMinus}, {"*", TokenStar}, {"/", TokenSlash},
		{"%", TokenPercent}, {"|", TokenPipe}, {",", TokenComma},
		{"(", TokenLParen}, {")", TokenRParen}, {"[", TokenLBrack}, {"]", TokenRBrack},
		{"![", TokenMissing}, {".", TokenDot}, {"?", TokenOptional},
	}
	for _, tt := range tests {
		toks := lex(tt.input)
		if toks[0].Type != tt.want {
			t.Errorf("lex(%q)[0] = %v, want %v", tt.input, toks[0].Type, tt.want)
		}
	}
}

func TestLexTildeKeywords(t *testing.T) {
	toks := lex("a in~ (1) or b like~ \"x\" or c regex~ \"y\"")
	var got []TokenType
	for _, tok := range toks {
		if tok.Type == TokenInTilde || tok.Type == TokenLikeTilde || tok.Type == TokenRegexTilde {
			got = append(got, tok.Type)
		}
	}
	if len(got) != 3 {
		t.Errorf("tilde keywords = %v", got)
	}
}

func TestLexTildeFunction(t *testing.T) {
	toks := lex(`endsWith~(process.name, ".exe")`)
	if toks[0].Type != TokenIdent || !toks[0].Tilde || toks[0].Value != "endsWith" {
		t.Errorf("token = %+v", toks[0])
	}
	if toks[1].Type != TokenLParen {
		t.Errorf("expected ( after tilde ident, got %v", toks[1].Type)
	}
}

func TestLexNumbers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"42", "42"},
		{"3.14", "3.14"},
		{"1.", "1."},
		{".5", ".5"},
		{"1e10", "1e10"},
		{"1.5e-3", "1.5e-3"},
		{"2E+6", "2E+6"},
	}
	for _, tt := range tests {
		toks := lex(tt.input)
		if toks[0].Type != TokenNumber || toks[0].Text != tt.want {
			t.Errorf("lex(%q) = %v %q", tt.input, toks[0].Type, toks[0].Text)
		}
	}
	// 30s lexes as number + identifier (unit handled by parser).
	toks := lex("30s")
	if toks[0].Type != TokenNumber || toks[1].Type != TokenIdent {
		t.Errorf("30s = %v", tokenTypes(toks))
	}
}

func TestLexStringEscapes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"a\nb"`, "a\nb"},
		{`"a\tb"`, "a\tb"},
		{`"a\\b"`, `a\b`},
		{`"a\"b"`, `a"b`},
		{`"a\u{48}b"`, "aHb"},
		{`"a\u{1F600}"`, "a😀"},
		{`"unknown\qescape"`, `unknown\qescape`}, // permissive
		{`'single'`, "single"},
	}
	for _, tt := range tests {
		toks := lex(tt.input)
		if toks[0].Type != TokenString || toks[0].Value != tt.want {
			t.Errorf("lex(%s) value = %q, want %q", tt.input, toks[0].Value, tt.want)
		}
	}
}

func TestLexTripleQuoted(t *testing.T) {
	toks := lex(`"""C:\Windows\*"""`)
	if toks[0].Type != TokenString || !toks[0].Raw || !toks[0].TripleQuoted {
		t.Fatalf("token = %+v", toks[0])
	}
	if toks[0].Value != `C:\Windows\*` {
		t.Errorf("value = %q", toks[0].Value)
	}
}

func TestLexTripleQuotedExtraQuotes(t *testing.T) {
	// """a"""" — one extra quote folds into the content per Elasticsearch.
	toks := lex(`"""a""""`)
	if toks[0].Value != `a"` {
		t.Errorf("value = %q, want a\"", toks[0].Value)
	}
}

func TestLexLegacyRawString(t *testing.T) {
	toks := lex(`?"C:\Users\*"`)
	if toks[0].Type != TokenString || !toks[0].Raw {
		t.Fatalf("token = %+v", toks[0])
	}
	if toks[0].Value != `C:\Users\*` {
		t.Errorf("value = %q", toks[0].Value)
	}
}

func TestLexOptionalVsRawString(t *testing.T) {
	// ? before an identifier is the optional marker, before a quote a raw string.
	toks := lex(`?user.name`)
	if toks[0].Type != TokenOptional || toks[1].Type != TokenIdent {
		t.Errorf("tokens = %v", tokenTypes(toks))
	}
}

func TestLexBacktickIdent(t *testing.T) {
	toks := lex("`my-field.sub` == 1")
	if toks[0].Type != TokenBacktickIdent || toks[0].Value != "my-field.sub" {
		t.Fatalf("token = %+v", toks[0])
	}
	// Escaped backtick.
	toks = lex("`a``b`")
	if toks[0].Value != "a`b" {
		t.Errorf("value = %q", toks[0].Value)
	}
}

func TestLexComments(t *testing.T) {
	toks := lex(`process // trailing comment
where /* block
comment */ true`)
	want := []TokenType{TokenIdent, TokenWhere, TokenTrue, TokenEOF}
	got := tokenTypes(toks)
	if len(got) != len(want) {
		t.Fatalf("tokens = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLexToleratedForeignOperators(t *testing.T) {
	toks := lex("a && b || !c")
	if toks[1].Type != TokenAnd || toks[3].Type != TokenOr || toks[4].Type != TokenNot {
		t.Errorf("tokens = %v", tokenTypes(toks))
	}
}

func TestLexUnterminatedForms(t *testing.T) {
	for _, input := range []string{`"abc`, `"""abc`, "`abc", `'abc`, "/* abc"} {
		l := newLexer(input)
		if len(l.errs) == 0 {
			t.Errorf("lex(%q): expected error", input)
		}
		// Must still terminate with EOF.
		if l.tokens[len(l.tokens)-1].Type != TokenEOF {
			t.Errorf("lex(%q): missing EOF", input)
		}
	}
}

func TestLexNewlineTerminatesString(t *testing.T) {
	l := newLexer("\"abc\ndef\"")
	if len(l.errs) == 0 {
		t.Error("expected unterminated string error")
	}
	// def parses as an identifier, trailing quote errors too.
	found := false
	for _, tok := range l.tokens {
		if tok.Type == TokenIdent && tok.Text == "def" {
			found = true
		}
	}
	if !found {
		t.Errorf("tokens = %v", l.tokens)
	}
}

func TestLexMultibyteInString(t *testing.T) {
	// Regression: a multi-byte rune inside a string must survive intact.
	// advance() moves past the whole rune, so the lexer must not capture it
	// one byte at a time.
	tests := []struct {
		input string
		want  string
	}{
		{`"café"`, "café"},
		{`"日本語"`, "日本語"},
		{`"emoji😀here"`, "emoji😀here"},
		{`"rtl‮override"`, "rtl‮override"},
		{"`fïeld`", "fïeld"},
	}
	for _, tt := range tests {
		toks := lex(tt.input)
		if toks[0].Value != tt.want {
			t.Errorf("lex(%s) value = %q, want %q", tt.input, toks[0].Value, tt.want)
		}
	}
}

func TestLexRadixIntegers(t *testing.T) {
	for _, in := range []string{"0x1BB", "0XFF", "0o755", "0b1010"} {
		toks := lex(in)
		if toks[0].Type != TokenNumber || toks[0].Text != in {
			t.Errorf("lex(%q) = %v %q", in, toks[0].Type, toks[0].Text)
		}
		if toks[1].Type != TokenEOF {
			t.Errorf("lex(%q) trailing token %v", in, toks[1].Type)
		}
	}
}

func TestLexUnicodeIdent(t *testing.T) {
	toks := lex("dätei where true")
	if toks[0].Type != TokenIdent || toks[0].Text != "dätei" {
		t.Errorf("token = %+v", toks[0])
	}
}

func TestLexPositions(t *testing.T) {
	toks := lex("a\n  b")
	if toks[0].Line != 1 || toks[0].Col != 1 {
		t.Errorf("a at %d:%d", toks[0].Line, toks[0].Col)
	}
	if toks[1].Line != 2 || toks[1].Col != 3 {
		t.Errorf("b at %d:%d", toks[1].Line, toks[1].Col)
	}
}

func TestLexErrorTokenForGarbage(t *testing.T) {
	l := newLexer("a @ b # c")
	if len(l.errs) < 2 {
		t.Errorf("errs = %v", l.errs)
	}
	// Identifiers still lexed around the garbage.
	var idents []string
	for _, tok := range l.tokens {
		if tok.Type == TokenIdent {
			idents = append(idents, tok.Text)
		}
	}
	if strings.Join(idents, ",") != "a,b,c" {
		t.Errorf("idents = %v", idents)
	}
}
