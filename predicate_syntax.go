package eql

import "unicode/utf8"

// canonicalExprWithBudget reserves a conservative rendering bound before the
// renderer allocates. The same call budget covers its preflight and rendering.
func canonicalExprWithBudget(e Expr, budget *parseBudget) string {
	budget.reserveRenderBytes(canonicalExprBound(e, budget))
	return ExprString(e)
}
func canonicalExprBound(e Expr, budget *parseBudget) int {
	budget.check(1)
	n := 32 // punctuation, operators, and necessary grouping per node
	switch v := e.(type) {
	case nil:
		return 0
	case *Binary:
		n += canonicalExprBound(v.L, budget) + canonicalExprBound(v.R, budget) + len(v.Op)
	case *Not:
		n += canonicalExprBound(v.X, budget)
	case *Paren:
		n += canonicalExprBound(v.X, budget)
	case *Unary:
		n += len(v.Op) + canonicalExprBound(v.X, budget)
	case *InExpr:
		n += canonicalExprBound(v.X, budget)
		for _, x := range v.List {
			n += canonicalExprBound(x, budget)
		}
	case *PatternExpr:
		n += canonicalExprBound(v.X, budget)
		for _, x := range v.Patterns {
			n += canonicalExprBound(x, budget)
		}
	case *Call:
		n += len(v.Name)
		for _, x := range v.Args {
			n += canonicalExprBound(x, budget)
		}
	case *Field:
		for _, p := range v.Path {
			budget.check(1)
			n += 32 + 2*len(p.Name)
		}
	case *Literal:
		if v.Kind == LitString {
			n += quotedStringBound(v.Value, budget)
		} else {
			n += len(v.Text)
		}
	case *Lineage:
		if v.Sub != nil {
			n += 6*len(v.Sub.Category) + canonicalExprBound(v.Sub.Where, budget)
		}
	}
	return n
}

func quotedStringBound(value string, budget *parseBudget) int {
	n := 2
	for len(value) > 0 {
		budget.check(1)
		r, size := utf8.DecodeRuneInString(value)
		switch {
		case r == utf8.RuneError && size == 1:
			n += 3
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == '\t':
			n += 2
		case r < 0x20:
			n += 8
		default:
			n += size
		}
		value = value[size:]
	}
	return n
}
