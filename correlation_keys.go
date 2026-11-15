package eql

// CorrelationKey preserves one authored by-expression. Syntax is canonical
// EQL and is authoritative for the complete key expression. It is not
// evaluated and does not establish backend support.
type CorrelationKey struct {
	Syntax string               `json:"syntax"`
	Field  *CorrelationKeyField `json:"field,omitempty"`
}

// CorrelationKeyField is present only when the by-expression, after removing
// outer parentheses, is a direct field. Name is the existing canonical-name
// projection; Syntax on the parent preserves quoted/indexed path identity.
type CorrelationKeyField struct {
	Name     string `json:"name"`
	Optional bool   `json:"optional,omitempty"`
}

func correlationKeyFromExpr(e Expr, budget *parseBudget) CorrelationKey {
	budget.node()
	syntax := canonicalExprWithBudget(e, budget)
	budget.addOutputExact(jsonTextBound(syntax, budget))
	key := CorrelationKey{Syntax: syntax}
	if field, ok := unwrap(e).(*Field); ok {
		// Field.Name builds a new string. Preflight its maximum path length
		// before that allocation, including separators and decimal indices.
		nameBytes, nameJSONBound := 0, 0
		for _, segment := range field.Path {
			budget.check(1)
			nameBytes += len(segment.Name) + 32
			nameJSONBound += jsonTextBound(segment.Name, budget) + 32
		}
		budget.reserveRenderBytes(nameBytes)
		budget.node()
		budget.addOutputExact(nameJSONBound)
		name := field.Name()
		key.Field = &CorrelationKeyField{Name: name, Optional: field.Optional}
	}
	return key
}

func cloneCorrelationKeysWithBudget(keys []CorrelationKey, budget *parseBudget) []CorrelationKey {
	budget.check(1)
	if len(keys) == 0 {
		return nil
	}
	// Account for every descriptor before allocating the destination array.
	for _, key := range keys {
		budget.node()
		budget.addOutputExact(jsonTextBound(key.Syntax, budget))
		if key.Field != nil {
			budget.node()
			budget.addOutputExact(jsonTextBound(key.Field.Name, budget))
		}
	}
	clone := make([]CorrelationKey, len(keys))
	for i, key := range keys {
		budget.check(1)
		clone[i] = key
		if key.Field != nil {
			field := *key.Field
			clone[i].Field = &field
		}
	}
	return clone
}
