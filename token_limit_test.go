package eql

import (
	"errors"
	"strings"
	"testing"
)

// Every token form counts against MaxTokens, not only the ones the lexer
// builds through emit.
func TestTokenLimitCountsEveryTokenForm(t *testing.T) {
	for _, form := range []string{`""`, `''`, `?""`, `""""""`, "`a`", `f~`} {
		query := "a where x in (" + strings.Repeat(form+" ", MaxTokens+1000) + ")"
		if _, err := Parse(query); !errors.Is(err, ErrResourceLimit) {
			t.Errorf("%s tokens: Parse error = %.80v, want ErrResourceLimit", form, err)
		}
		res := ExtractConditions(query)
		if len(res.Conditions) != 0 || len(res.Errors) != 1 || res.Errors[0] != ErrResourceLimit.Error()+" (tokens)" {
			t.Errorf("%s tokens: ExtractConditions = %d conditions, errors %.80q", form, len(res.Conditions), res.Errors)
		}
	}
}
