package eql

// canonicalExprWithBudget reserves a conservative rendering bound before the
// renderer allocates. The same call budget covers its preflight and rendering.
func canonicalExprWithBudget(e Expr, budget *parseBudget) string {
	budget.addOutput(canonicalExprBound(e, budget))
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
		n += 6*len(v.Value) + len(v.Text)
	case *Lineage:
		if v.Sub != nil {
			n += 6*len(v.Sub.Category) + canonicalExprBound(v.Sub.Where, budget)
		}
	}
	return n
}
