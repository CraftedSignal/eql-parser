package eql

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// lexer tokenizes EQL input. It is permissive: malformed input produces
// error tokens and messages in errs rather than aborting, so the parser can
// recover and extract what it can.
type lexer struct {
	input  string
	pos    int
	line   int
	col    int
	tokens []Token
	errs   []string
}

func newLexer(input string) *lexer {
	l := &lexer{input: input, line: 1, col: 1}
	l.run()
	return l
}

func (l *lexer) errorf(line, col int, format string, args ...any) {
	l.errs = append(l.errs, fmt.Sprintf("line %d:%d: %s", line, col, fmt.Sprintf(format, args...)))
}

// emit appends a token whose raw text spans [start, l.pos).
func (l *lexer) emit(typ TokenType, start, startLine, startCol int, value string) {
	l.tokens = append(l.tokens, Token{
		Type:  typ,
		Text:  l.input[start:l.pos],
		Value: value,
		Pos:   start,
		Line:  startLine,
		Col:   startCol,
	})
}

// advance moves past the byte (or rune) at l.pos, maintaining line/col.
func (l *lexer) advance() {
	if l.pos >= len(l.input) {
		return
	}
	if l.input[l.pos] == '\n' {
		l.line++
		l.col = 1
		l.pos++
		return
	}
	// l.pos < len(l.input) here (guarded above), so the slice is non-empty and
	// DecodeRuneInString always returns a size >= 1.
	_, size := utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += size
	l.col++
}

func (l *lexer) peek(offset int) byte {
	if l.pos+offset >= len(l.input) {
		return 0
	}
	return l.input[l.pos+offset]
}

func (l *lexer) run() {
	for l.pos < len(l.input) {
		startLine, startCol := l.line, l.col
		start := l.pos
		c := l.input[l.pos]

		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f':
			l.advance()

		case c == '/' && l.peek(1) == '/':
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.advance()
			}

		case c == '/' && l.peek(1) == '*':
			l.advance()
			l.advance()
			closed := false
			for l.pos < len(l.input) {
				if l.input[l.pos] == '*' && l.peek(1) == '/' {
					l.advance()
					l.advance()
					closed = true
					break
				}
				l.advance()
			}
			if !closed {
				l.errorf(startLine, startCol, "unterminated block comment")
			}

		case c == '"':
			l.lexString(start, startLine, startCol, '"', false)

		case c == '\'':
			l.lexString(start, startLine, startCol, '\'', false)

		case c == '?' && (l.peek(1) == '"' || l.peek(1) == '\''):
			// Legacy Endgame raw string: ?"no\escapes" or ?'...'
			l.advance() // consume ?
			l.lexString(start, startLine, startCol, l.input[l.pos], true)

		case c == '`':
			l.lexBacktickIdent(start, startLine, startCol)

		case c >= '0' && c <= '9':
			l.lexNumber(start, startLine, startCol)

		case c == '.' && l.peek(1) >= '0' && l.peek(1) <= '9':
			l.lexNumber(start, startLine, startCol)

		case c == '_' || unicode.IsLetter(firstRune(l.input[l.pos:])):
			l.lexIdent(start, startLine, startCol)

		default:
			l.lexOperator(start, startLine, startCol)
		}
	}
	l.tokens = append(l.tokens, Token{Type: TokenEOF, Pos: l.pos, Line: l.line, Col: l.col})
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func (l *lexer) lexOperator(start, startLine, startCol int) {
	c := l.input[l.pos]
	two := ""
	if l.pos+1 < len(l.input) {
		two = l.input[l.pos : l.pos+2]
	}

	switch two {
	case "==":
		l.advance()
		l.advance()
		l.emit(TokenEQ, start, startLine, startCol, "==")
		return
	case "!=":
		l.advance()
		l.advance()
		l.emit(TokenNEQ, start, startLine, startCol, "!=")
		return
	case "<=":
		l.advance()
		l.advance()
		l.emit(TokenLTE, start, startLine, startCol, "<=")
		return
	case ">=":
		l.advance()
		l.advance()
		l.emit(TokenGTE, start, startLine, startCol, ">=")
		return
	case "![":
		l.advance()
		l.advance()
		l.emit(TokenMissing, start, startLine, startCol, "![")
		return
	case "&&": // not EQL, but tolerated: common paste-in from other languages
		l.advance()
		l.advance()
		l.emit(TokenAnd, start, startLine, startCol, "and")
		return
	case "||":
		l.advance()
		l.advance()
		l.emit(TokenOr, start, startLine, startCol, "or")
		return
	}

	var typ TokenType
	switch c {
	case '<':
		typ = TokenLT
	case '>':
		typ = TokenGT
	case ':':
		typ = TokenColon
	case '=':
		typ = TokenAssign
	case '+':
		typ = TokenPlus
	case '-':
		typ = TokenMinus
	case '*':
		typ = TokenStar
	case '/':
		typ = TokenSlash
	case '%':
		typ = TokenPercent
	case '|':
		typ = TokenPipe
	case ',':
		typ = TokenComma
	case '(':
		typ = TokenLParen
	case ')':
		typ = TokenRParen
	case '[':
		typ = TokenLBrack
	case ']':
		typ = TokenRBrack
	case '.':
		typ = TokenDot
	case '?':
		typ = TokenOptional
	case '!':
		// Bare ! is not EQL, but content pasted from C-like languages uses it
		// for negation. Tolerate it as "not" so extraction still works.
		l.advance()
		l.emit(TokenNot, start, startLine, startCol, "not")
		return
	default:
		l.advance()
		l.errorf(startLine, startCol, "unexpected character %q", l.input[start:l.pos])
		l.emit(TokenError, start, startLine, startCol, l.input[start:l.pos])
		return
	}
	l.advance()
	l.emit(typ, start, startLine, startCol, l.input[start:l.pos])
}

func (l *lexer) lexIdent(start, startLine, startCol int) {
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			l.advance()
			continue
		}
		if c >= utf8.RuneSelf && unicode.IsLetter(firstRune(l.input[l.pos:])) {
			l.advance()
			continue
		}
		break
	}
	word := l.input[start:l.pos]
	lower := strings.ToLower(word)

	// A directly attached ~ makes a case-insensitive keyword (in~, like~,
	// regex~) or function name (endsWith~).
	hasTilde := l.pos < len(l.input) && l.input[l.pos] == '~'

	if typ, ok := keywords[lower]; ok {
		if hasTilde {
			switch typ {
			case TokenIn:
				l.advance()
				l.emit(TokenInTilde, start, startLine, startCol, "in~")
				return
			case TokenLike:
				l.advance()
				l.emit(TokenLikeTilde, start, startLine, startCol, "like~")
				return
			case TokenRegex:
				l.advance()
				l.emit(TokenRegexTilde, start, startLine, startCol, "regex~")
				return
			}
			// Other keyword followed by ~ is malformed; emit the keyword and
			// let the operator path complain about the tilde.
		}
		l.emit(typ, start, startLine, startCol, lower)
		return
	}

	if hasTilde {
		l.advance()
		l.tokens = append(l.tokens, Token{
			Type:  TokenIdent,
			Text:  l.input[start:l.pos],
			Value: word,
			Pos:   start,
			Line:  startLine,
			Col:   startCol,
			Tilde: true,
		})
		return
	}
	l.emit(TokenIdent, start, startLine, startCol, word)
}

func (l *lexer) lexNumber(start, startLine, startCol int) {
	// Radix-prefixed integers (0x.., 0o.., 0b..). Not standard EQL, but they
	// appear in content ported from other query languages; accept them so
	// extraction is not blocked.
	if l.input[l.pos] == '0' && l.pos+1 < len(l.input) {
		switch l.input[l.pos+1] {
		case 'x', 'X':
			l.advance()
			l.advance()
			for l.pos < len(l.input) && isHexDigit(l.input[l.pos]) {
				l.advance()
			}
			l.emit(TokenNumber, start, startLine, startCol, l.input[start:l.pos])
			return
		case 'o', 'O':
			l.advance()
			l.advance()
			for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '7' {
				l.advance()
			}
			l.emit(TokenNumber, start, startLine, startCol, l.input[start:l.pos])
			return
		case 'b', 'B':
			l.advance()
			l.advance()
			for l.pos < len(l.input) && (l.input[l.pos] == '0' || l.input[l.pos] == '1') {
				l.advance()
			}
			l.emit(TokenNumber, start, startLine, startCol, l.input[start:l.pos])
			return
		}
	}

	seenDot := false
	seenExp := false
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		switch {
		case c >= '0' && c <= '9':
			l.advance()
		case c == '.' && !seenDot && !seenExp:
			// Only part of the number if followed by a digit or at a
			// position like "1." (trailing decimal allowed).
			seenDot = true
			l.advance()
		case (c == 'e' || c == 'E') && !seenExp:
			// Exponent only if followed by digit or sign+digit.
			next := l.peek(1)
			if next >= '0' && next <= '9' {
				seenExp = true
				l.advance()
			} else if (next == '+' || next == '-') && l.peek(2) >= '0' && l.peek(2) <= '9' {
				seenExp = true
				l.advance()
				l.advance()
			} else {
				goto done
			}
		default:
			goto done
		}
	}
done:
	l.emit(TokenNumber, start, startLine, startCol, l.input[start:l.pos])
}

// lexBacktickIdent reads `field-name` with “ escaping a literal backtick.
func (l *lexer) lexBacktickIdent(start, startLine, startCol int) {
	l.advance() // opening backtick
	var val strings.Builder
	closed := false
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == '`' {
			if l.peek(1) == '`' {
				val.WriteByte('`')
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			closed = true
			break
		}
		runeStart := l.pos
		l.advance()
		val.WriteString(l.input[runeStart:l.pos])
	}
	if !closed {
		l.errorf(startLine, startCol, "unterminated backtick identifier")
	}
	l.tokens = append(l.tokens, Token{
		Type:  TokenBacktickIdent,
		Text:  l.input[start:l.pos],
		Value: val.String(),
		Pos:   start,
		Line:  startLine,
		Col:   startCol,
	})
}

// lexString handles all string forms. quote is the delimiter byte at l.pos.
// raw disables escape processing (legacy ?"..." / ?'...' form).
func (l *lexer) lexString(start, startLine, startCol int, quote byte, raw bool) {
	// Triple-quoted raw string: """..."""
	if quote == '"' && l.peek(1) == '"' && l.peek(2) == '"' {
		l.lexTripleString(start, startLine, startCol)
		return
	}

	l.advance() // opening quote
	var val strings.Builder
	closed := false
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == quote {
			l.advance()
			closed = true
			break
		}
		if c == '\n' && !raw {
			// EQL strings are single-line; treat a bare newline as
			// termination so one bad string doesn't eat the whole query.
			break
		}
		if c == '\\' && !raw {
			l.lexEscape(&val)
			continue
		}
		// Write the whole rune: advance() moves past a multi-byte rune, so
		// writing a single byte here would truncate UTF-8.
		runeStart := l.pos
		l.advance()
		val.WriteString(l.input[runeStart:l.pos])
	}
	if !closed {
		l.errorf(startLine, startCol, "unterminated string")
	}
	l.tokens = append(l.tokens, Token{
		Type:         TokenString,
		Text:         l.input[start:l.pos],
		Value:        val.String(),
		Pos:          start,
		Line:         startLine,
		Col:          startCol,
		Raw:          raw,
		SingleQuoted: quote == '\'',
	})
}

// lexEscape decodes one backslash escape at l.pos into val. Unknown escapes
// are kept literally (backslash and all) so patterns like Windows paths in
// sloppy content survive extraction.
func (l *lexer) lexEscape(val *strings.Builder) {
	l.advance() // backslash
	if l.pos >= len(l.input) {
		val.WriteByte('\\')
		return
	}
	c := l.input[l.pos]
	switch c {
	case 'n':
		val.WriteByte('\n')
	case 'r':
		val.WriteByte('\r')
	case 't':
		val.WriteByte('\t')
	case 'b':
		val.WriteByte('\b')
	case 'f':
		val.WriteByte('\f')
	case '\\':
		val.WriteByte('\\')
	case '"':
		val.WriteByte('"')
	case '\'':
		val.WriteByte('\'')
	case 'u':
		// Modern EQL unicode escape: \u{2-8 hex digits} (accept 1-8).
		if l.peek(1) == '{' {
			end := l.pos + 2
			hexStart := end
			for end < len(l.input) && end-hexStart <= 8 && isHexDigit(l.input[end]) {
				end++
			}
			if end < len(l.input) && l.input[end] == '}' && end > hexStart {
				var cp rune
				for _, h := range []byte(l.input[hexStart:end]) {
					cp = cp*16 + rune(hexValue(h))
				}
				if utf8.ValidRune(cp) {
					val.WriteRune(cp)
				} else {
					val.WriteRune(utf8.RuneError)
				}
				for l.pos <= end {
					l.advance()
				}
				return
			}
		}
		// Not a valid \u{...} sequence: keep literally.
		val.WriteByte('\\')
		val.WriteByte('u')
	default:
		val.WriteByte('\\')
		val.WriteByte(c)
	}
	l.advance()
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// lexTripleString reads """...""". Content is raw (no escapes). Following
// Elasticsearch, up to two extra closing quotes are folded into the content,
// so """a"""" decodes to a".
func (l *lexer) lexTripleString(start, startLine, startCol int) {
	l.advance()
	l.advance()
	l.advance() // opening """
	contentStart := l.pos
	closed := false
	contentEnd := -1
	for l.pos < len(l.input) {
		if l.input[l.pos] == '"' && l.peek(1) == '"' && l.peek(2) == '"' {
			// Candidate close; absorb up to two extra quotes into content.
			extra := 0
			for extra < 2 && l.pos+3+extra < len(l.input) && l.input[l.pos+3+extra] == '"' {
				extra++
			}
			for i := 0; i < extra; i++ {
				l.advance()
			}
			contentEnd = l.pos
			l.advance()
			l.advance()
			l.advance() // closing """
			closed = true
			break
		}
		l.advance()
	}
	if !closed {
		contentEnd = l.pos
		l.errorf(startLine, startCol, "unterminated raw string")
	}
	l.tokens = append(l.tokens, Token{
		Type:         TokenString,
		Text:         l.input[start:l.pos],
		Value:        l.input[contentStart:contentEnd],
		Pos:          start,
		Line:         startLine,
		Col:          startCol,
		Raw:          true,
		TripleQuoted: true,
	})
}
