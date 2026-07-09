package eql

import "fmt"

// TokenType identifies the lexical class of a token.
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenError

	// Literals and names
	TokenIdent         // process, endsWith, endsWith~ (Tilde=true)
	TokenBacktickIdent // `my-field` (Value holds the unescaped name)
	TokenString        // "...", """...""", '...', ?"..." (Value holds the decoded value)
	TokenNumber        // 42, 3.14, 1e-5, .5

	// Keywords
	TokenAnd
	TokenOr
	TokenNot
	TokenIn         // in
	TokenInTilde    // in~
	TokenLike       // like
	TokenLikeTilde  // like~
	TokenRegex      // regex
	TokenRegexTilde // regex~
	TokenWhere
	TokenSequence
	TokenJoin
	TokenSample
	TokenUntil
	TokenBy
	TokenWith
	TokenMaxspan
	TokenOf
	TokenAny
	TokenTrue
	TokenFalse
	TokenNull

	// Operators and punctuation
	TokenEQ       // ==
	TokenNEQ      // !=
	TokenLT       // <
	TokenLTE      // <=
	TokenGT       // >
	TokenGTE      // >=
	TokenColon    // :
	TokenAssign   // =
	TokenPlus     // +
	TokenMinus    // -
	TokenStar     // *
	TokenSlash    // /
	TokenPercent  // %
	TokenPipe     // |
	TokenComma    // ,
	TokenLParen   // (
	TokenRParen   // )
	TokenLBrack   // [
	TokenRBrack   // ]
	TokenMissing  // ![ (missing-event subquery opener)
	TokenDot      // .
	TokenOptional // ? (optional field marker)
)

var tokenNames = map[TokenType]string{
	TokenEOF:           "EOF",
	TokenError:         "error",
	TokenIdent:         "identifier",
	TokenBacktickIdent: "backtick identifier",
	TokenString:        "string",
	TokenNumber:        "number",
	TokenAnd:           "and",
	TokenOr:            "or",
	TokenNot:           "not",
	TokenIn:            "in",
	TokenInTilde:       "in~",
	TokenLike:          "like",
	TokenLikeTilde:     "like~",
	TokenRegex:         "regex",
	TokenRegexTilde:    "regex~",
	TokenWhere:         "where",
	TokenSequence:      "sequence",
	TokenJoin:          "join",
	TokenSample:        "sample",
	TokenUntil:         "until",
	TokenBy:            "by",
	TokenWith:          "with",
	TokenMaxspan:       "maxspan",
	TokenOf:            "of",
	TokenAny:           "any",
	TokenTrue:          "true",
	TokenFalse:         "false",
	TokenNull:          "null",
	TokenEQ:            "==",
	TokenNEQ:           "!=",
	TokenLT:            "<",
	TokenLTE:           "<=",
	TokenGT:            ">",
	TokenGTE:           ">=",
	TokenColon:         ":",
	TokenAssign:        "=",
	TokenPlus:          "+",
	TokenMinus:         "-",
	TokenStar:          "*",
	TokenSlash:         "/",
	TokenPercent:       "%",
	TokenPipe:          "|",
	TokenComma:         ",",
	TokenLParen:        "(",
	TokenRParen:        ")",
	TokenLBrack:        "[",
	TokenRBrack:        "]",
	TokenMissing:       "![",
	TokenDot:           ".",
	TokenOptional:      "?",
}

func (t TokenType) String() string {
	if s, ok := tokenNames[t]; ok {
		return s
	}
	return fmt.Sprintf("token(%d)", int(t))
}

// keywords maps lowercase keyword text to its token type. EQL keywords are
// lowercase; the lexer matches case-insensitively for robustness against
// hand-edited content and records the original spelling in Token.Text.
var keywords = map[string]TokenType{
	"and":      TokenAnd,
	"or":       TokenOr,
	"not":      TokenNot,
	"in":       TokenIn,
	"like":     TokenLike,
	"regex":    TokenRegex,
	"where":    TokenWhere,
	"sequence": TokenSequence,
	"join":     TokenJoin,
	"sample":   TokenSample,
	"until":    TokenUntil,
	"by":       TokenBy,
	"with":     TokenWith,
	"maxspan":  TokenMaxspan,
	"of":       TokenOf,
	"any":      TokenAny,
	"true":     TokenTrue,
	"false":    TokenFalse,
	"null":     TokenNull,
}

// Token is a single lexical unit with position information.
type Token struct {
	Type  TokenType
	Text  string // raw source text (original spelling)
	Value string // decoded value for strings and backtick identifiers, lowercase-insensitive-normalized for keywords
	Pos   int    // byte offset in input
	Line  int    // 1-based line
	Col   int    // 1-based column (bytes)

	Tilde        bool // identifier carried a ~ suffix (case-insensitive function)
	Raw          bool // string was raw (triple-quoted or ?"..." form)
	TripleQuoted bool // string used """..."""
	SingleQuoted bool // string used '...' (legacy Endgame form)
}

// isKeyword reports whether the token is any reserved keyword.
func (t Token) isKeyword() bool {
	switch t.Type {
	case TokenAnd, TokenOr, TokenNot, TokenIn, TokenInTilde, TokenLike, TokenLikeTilde,
		TokenRegex, TokenRegexTilde, TokenWhere, TokenSequence, TokenJoin, TokenSample,
		TokenUntil, TokenBy, TokenWith, TokenMaxspan, TokenOf, TokenAny, TokenTrue,
		TokenFalse, TokenNull:
		return true
	}
	return false
}
