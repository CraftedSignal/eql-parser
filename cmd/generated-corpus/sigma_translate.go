package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	eql "github.com/craftedsignal/eql-parser"
	sigma "github.com/craftedsignal/sigma-parser"
)

// This file implements a bidirectional Sigma <-> EQL translator. Neither
// parser package emits text (both are parse-only), so — following the SPL/KQL
// precedent — all translation and serialization lives here in the generator
// command, keeping the eql-parser core dependency-free.
//
// Correctness is defined by faithful round-tripping: translating a rule to the
// other dialect and re-parsing it must yield the same *set of conditions*,
// under a shared canonical vocabulary that folds representational differences
// (`:` vs `==`, `contains` vs `*x*`, etc.) into one form. A translation that
// would lose or invent a condition fails.

// translateability classifies how fully a rule can cross the Sigma/EQL divide.
type translateability string

const (
	transField     translateability = "field"       // pure field conditions — fully translatable
	transKeyword   translateability = "keyword"     // free-text keyword search — no EQL analog
	transAggregate translateability = "aggregation" // count()/near — partial (conditions only)
	transComplex   translateability = "complex"     // escaped wildcards — no faithful EQL form
	transEmpty     translateability = "empty"       // nothing to translate
)

// canonCond is the shared canonical form both dialects fold into for
// comparison. Op is one of: eq, contains, startswith, endswith, matches,
// cidrmatch, gt, ge, lt, le, exists_true, exists_false, fieldref.
type canonCond struct {
	Field   string
	Op      string
	Value   string
	Negated bool
	CI      bool // case-insensitive match
}

func (c canonCond) key() string {
	return fmt.Sprintf("%v|%s|%s|%s|%v", c.Negated, c.Field, c.Op, c.Value, c.CI)
}

// ---------------------------------------------------------------------------
// Sigma -> EQL
// ---------------------------------------------------------------------------

// SigmaToEQL translates a parsed Sigma rule into an EQL query string, plus a
// classification of how completely it could be translated. Field conditions
// translate fully; keyword and aggregation constructs are reported so the
// caller never mistakes a lossy translation for a faithful one.
func SigmaToEQL(sig *sigma.ParseResult) (query string, class translateability) {
	if sig == nil || len(sig.Conditions) == 0 {
		return "any where true", transEmpty
	}
	if hasEscapedWildcard(sig) || hasNormalizationFragileValue(sig) {
		// Literal `\*`/`\?` have no EQL wildcard-operator form; NBSP/zero-width
		// characters are folded away by EQL's NormalizeQuery, so rules that
		// hinge on them (unicode-obfuscation detections) cannot round-trip
		// faithfully and are classified rather than silently mistranslated.
		return "any where true", transComplex
	}

	class = transField
	if len(sig.Commands) > 0 {
		class = transAggregate
	}

	var parts []string
	for i, c := range sig.Conditions {
		leaf, ok := sigmaCondToEQL(c)
		if !ok {
			// A keyword/free-text term: EQL cannot express it. Mark the rule
			// keyword-class and skip the term from the field translation.
			if c.Field == "" || c.Operator == "keyword" {
				class = transKeyword
			}
			continue
		}
		connector := " and "
		if i > 0 && c.LogicalOp == "OR" {
			connector = " or "
		}
		if len(parts) == 0 {
			parts = append(parts, leaf)
		} else {
			parts = append(parts, connector, leaf)
		}
	}
	if len(parts) == 0 {
		return "any where true", class
	}
	return "any where " + strings.Join(parts, ""), class
}

// sigmaCondToEQL renders one Sigma condition as an EQL boolean leaf. ok is
// false for terms with no EQL representation (keyword search).
func sigmaCondToEQL(c sigma.Condition) (string, bool) {
	if c.Field == "" || c.Operator == "keyword" {
		return "", false
	}
	field := eqlFieldName(c.Field)
	ci := !c.CaseSensitive // Sigma matches case-insensitively unless |cased
	values := c.Alternatives
	if len(values) == 0 {
		values = []string{c.Value}
	}

	var leaf string
	switch strings.ToLower(c.Operator) {
	case "=", "":
		leaf = eqLeaf(field, values, ci)
	case "contains":
		leaf = patternLeaf(field, values, "*", "*", ci)
	case "startswith":
		leaf = patternLeaf(field, values, "", "*", ci)
	case "endswith":
		leaf = patternLeaf(field, values, "*", "", ci)
	case "matches":
		leaf = regexLeaf(field, values, ci)
	case "cidrmatch":
		leaf = cidrLeaf(field, values)
	case ">", ">=", "<", "<=":
		leaf = field + " " + c.Operator + " " + numLiteral(c.Value)
	case "exists":
		if isTrue(c.Value) {
			leaf = field + " != null"
		} else {
			leaf = field + " == null"
		}
	case "fieldref":
		leaf = field + " == " + eqlFieldName(c.Value)
	default:
		return "", false
	}
	if c.Negated {
		leaf = "not (" + leaf + ")"
	}
	return leaf, true
}

// eqLeaf renders equality (case-insensitive `:` or case-sensitive `==`/list),
// honoring embedded wildcards.
func eqLeaf(field string, values []string, ci bool) string {
	if ci {
		return field + " : " + quoteList(values)
	}
	if anyWildcard(values) {
		return field + " like " + quoteList(values)
	}
	if len(values) == 1 {
		return field + " == " + eqlQuote(values[0])
	}
	return field + " in (" + strings.Join(quoteEach(values), ", ") + ")"
}

// patternLeaf wraps each value with the given prefix/suffix wildcards and
// emits a `:` (CI) or `like` (CS) match.
func patternLeaf(field string, values []string, pre, suf string, ci bool) string {
	wrapped := make([]string, len(values))
	for i, v := range values {
		wrapped[i] = pre + v + suf
	}
	op := " like "
	if ci {
		op = " : "
	}
	return field + op + quoteList(wrapped)
}

func regexLeaf(field string, values []string, ci bool) string {
	op := " regex "
	if ci {
		op = " regex~ "
	}
	return field + op + quoteList(values)
}

func cidrLeaf(field string, values []string) string {
	args := append([]string{field}, quoteEach(values)...)
	return "cidrMatch(" + strings.Join(args, ", ") + ")"
}

// ---------------------------------------------------------------------------
// EQL -> Sigma
// ---------------------------------------------------------------------------

// eqlSigmaRule is the minimal Sigma document we emit.
type eqlSigmaRule struct {
	Title     string            `yaml:"title"`
	Status    string            `yaml:"status"`
	Logsource map[string]string `yaml:"logsource"`
	Detection map[string]any    `yaml:"detection"`
}

// EQLToSigmaDetection builds a Sigma detection map from an EQL result. Returns
// ok=false when the query is a correlation (sequence/join/sample) or uses only
// constructs Sigma cannot represent - Sigma has no ordered-sequence analog.
//
// Each EQL condition becomes one or more Sigma selections OR'd together (a
// value list with mixed wildcard shapes is split into a selection per shape),
// and the conditions are AND'd - with negation and existence handled in the
// condition expression so nothing is silently mistranslated.
func EQLToSigmaDetection(res *eql.ParseResult) (map[string]any, translateability, bool) {
	if res == nil {
		return nil, transEmpty, false
	}
	if res.Sequence != nil {
		// Ordered/unordered multi-event correlation has no classic-Sigma form.
		return nil, translateability("correlation"), false
	}
	if len(res.Conditions) == 0 {
		return nil, transEmpty, false
	}

	det := map[string]any{}
	class := transField
	var terms []string
	idx := 0
	for _, c := range res.Conditions {
		if c.PipeStage > 0 {
			class = transAggregate
			continue
		}
		units, ok := eqlCondToSigmaUnits(c)
		if !ok || len(units) == 0 {
			return nil, transKeyword, false
		}
		var names []string
		for j, u := range units {
			name := fmt.Sprintf("sel%d_%d", idx, j)
			det[name] = u
			names = append(names, name)
		}
		idx++
		term := strings.Join(names, " or ")
		if len(names) > 1 {
			term = "(" + term + ")"
		}
		if c.Negated {
			if len(names) == 1 {
				term = "not " + names[0]
			} else {
				term = "not " + term
			}
		}
		terms = append(terms, term)
	}
	if len(det) == 0 {
		return nil, class, false
	}
	det["condition"] = strings.Join(terms, " and ")
	return det, class, true
}

// eqlCondToSigmaUnits renders one EQL condition as a set of Sigma selection
// maps that are OR'd together. Negation is handled by the caller via the
// condition expression, so units built here are always positive matches.
func eqlCondToSigmaUnits(c eql.Condition) ([]map[string]any, bool) {
	field := c.Field
	values := c.Alternatives
	if len(values) == 0 {
		values = []string{c.Value}
	}

	// Existence checks.
	if c.Value == "null" && len(c.Alternatives) == 0 {
		switch c.Operator {
		case "!=":
			return []map[string]any{{field: "*"}}, true // exists
		case "==":
			return []map[string]any{{field: nil}}, true // absent
		}
	}
	// Field-to-field reference.
	if c.ValueIsField {
		return []map[string]any{{field + "|fieldref": c.Value}}, true
	}

	casedSuffix := ""
	if !c.CaseInsensitive {
		casedSuffix = "|cased"
	}

	switch c.Operator {
	case ">", ">=", "<", "<=":
		mod := map[string]string{">": "|gt", ">=": "|gte", "<": "|lt", "<=": "|lte"}[c.Operator]
		return []map[string]any{{field + mod: c.Value}}, true
	case "cidrMatch", "cidrmatch":
		return []map[string]any{{field + "|cidr": listOrScalar(values)}}, true
	case "regex", "match":
		return []map[string]any{{field + "|re" + casedSuffix: listOrScalar(values)}}, true
	case "startsWith":
		return []map[string]any{{field + "|startswith" + casedSuffix: listOrScalar(values)}}, true
	case "endsWith":
		return []map[string]any{{field + "|endswith" + casedSuffix: listOrScalar(values)}}, true
	case "stringContains":
		return []map[string]any{{field + "|contains" + casedSuffix: listOrScalar(values)}}, true
	case "==", "=", ":", "in", "like", "wildcard":
		return shapeGroupedUnits(field, values, casedSuffix), true
	default:
		return nil, false
	}
}

// shapeGroupedUnits splits a value list into one Sigma selection per wildcard
// shape (plain / contains / startswith / endswith / exists), so a mixed list
// like ("a*", "*b*", "c") round-trips exactly instead of being forced to a
// single modifier.
func shapeGroupedUnits(field string, values []string, casedSuffix string) []map[string]any {
	type group struct {
		mod  string
		vals []string
	}
	var order []string
	groups := map[string]*group{}
	add := func(mod, v string) {
		g, ok := groups[mod]
		if !ok {
			g = &group{mod: mod}
			groups[mod] = g
			order = append(order, mod)
		}
		g.vals = append(g.vals, v)
	}
	for _, v := range values {
		body := strings.Trim(v, "*")
		if body == "" && strings.Contains(v, "*") {
			add("__exists__", "*")
			continue
		}
		pre := strings.HasPrefix(v, "*")
		suf := strings.HasSuffix(v, "*")
		switch {
		case pre && suf:
			add("|contains"+casedSuffix, body)
		case pre:
			add("|endswith"+casedSuffix, body)
		case suf:
			add("|startswith"+casedSuffix, body)
		default:
			add(casedSuffix, v)
		}
	}
	var units []map[string]any
	for _, mod := range order {
		if mod == "__exists__" {
			units = append(units, map[string]any{field: "*"})
			continue
		}
		units = append(units, map[string]any{field + mod: listOrScalar(groups[mod].vals)})
	}
	return units
}

// ---------------------------------------------------------------------------
// Canonical folding for round-trip comparison
// ---------------------------------------------------------------------------

// canonFromSigma folds a Sigma condition into canonical conditions.
func canonFromSigma(c sigma.Condition) []canonCond {
	if c.Field == "" || c.Operator == "keyword" {
		return nil
	}
	ci := !c.CaseSensitive
	values := c.Alternatives
	if len(values) == 0 {
		values = []string{c.Value}
	}
	var op string
	switch strings.ToLower(c.Operator) {
	case "=", "":
		return wildcardCanon(c.Field, values, c.Negated, ci)
	case "contains", "startswith", "endswith":
		op = strings.ToLower(c.Operator)
	case "matches":
		op = "matches"
	case "cidrmatch":
		op = "cidrmatch"
	case ">":
		op = "gt"
	case ">=":
		op = "ge"
	case "<":
		op = "lt"
	case "<=":
		op = "le"
	case "exists":
		if isTrue(c.Value) {
			return []canonCond{{Field: lc(c.Field), Op: "exists_true", Negated: c.Negated}}
		}
		return []canonCond{{Field: lc(c.Field), Op: "exists_false", Negated: c.Negated}}
	case "fieldref":
		return []canonCond{{Field: lc(c.Field), Op: "fieldref", Value: lc(c.Value), Negated: c.Negated}}
	default:
		return nil
	}
	return valueCanon(c.Field, op, values, c.Negated, ci)
}

// canonFromEQL folds an EQL condition into canonical conditions.
func canonFromEQL(c eql.Condition) []canonCond {
	ci := c.CaseInsensitive
	// Field-to-field comparison is a Sigma fieldref, not a value match.
	if c.ValueIsField {
		return []canonCond{{Field: lc(c.Field), Op: "fieldref", Value: lc(c.Value), Negated: c.Negated}}
	}
	// Null existence checks take priority — they are not value matches.
	if c.Value == "null" && len(c.Alternatives) == 0 {
		switch c.Operator {
		case "==", "=":
			return []canonCond{{Field: lc(c.Field), Op: "exists_false", Negated: c.Negated}}
		case "!=":
			return []canonCond{{Field: lc(c.Field), Op: "exists_true", Negated: c.Negated}}
		}
	}
	values := c.Alternatives
	if len(values) == 0 {
		values = []string{c.Value}
	}
	switch c.Operator {
	case "==", "=", ":", "in", "like", "wildcard":
		return wildcardCanon(c.Field, values, c.Negated, ci)
	case "regex", "match":
		return valueCanon(c.Field, "matches", values, c.Negated, ci)
	case "cidrMatch", "cidrmatch":
		return valueCanon(c.Field, "cidrmatch", values, c.Negated, ci)
	case "startsWith":
		return valueCanon(c.Field, "startswith", values, c.Negated, ci)
	case "endsWith":
		return valueCanon(c.Field, "endswith", values, c.Negated, ci)
	case "stringContains":
		return valueCanon(c.Field, "contains", values, c.Negated, ci)
	case ">":
		return valueCanon(c.Field, "gt", values, c.Negated, ci)
	case ">=":
		return valueCanon(c.Field, "ge", values, c.Negated, ci)
	case "<":
		return valueCanon(c.Field, "lt", values, c.Negated, ci)
	case "<=":
		return valueCanon(c.Field, "le", values, c.Negated, ci)
	case "!=":
		// field != v  ==  not (field == v)
		return wildcardCanon(c.Field, values, !c.Negated, ci)
	default:
		return nil
	}
}

// textOps are the match operators for which case-sensitivity is meaningful.
var textOps = map[string]bool{
	"eq": true, "contains": true, "startswith": true, "endswith": true, "matches": true,
}

// stripEdgeWildcards removes the wildcards a positional operator already
// implies (a `contains` value needn't be written `*x*`), so the two dialects'
// values compare equal regardless of redundant edge stars.
func stripEdgeWildcards(op, v string) string {
	switch op {
	case "contains":
		return strings.Trim(v, "*")
	case "startswith":
		return strings.TrimRight(v, "*")
	case "endswith":
		return strings.TrimLeft(v, "*")
	default:
		return v
	}
}

// wildcardCanon turns equality-like values into eq/contains/startswith/
// endswith canonical conditions based on wildcard placement. A value that is
// only wildcards (e.g. "*") means "field exists" rather than a text match.
func wildcardCanon(field string, values []string, negated, ci bool) []canonCond {
	var out []canonCond
	for _, v := range values {
		if v == "null" {
			continue
		}
		body := strings.Trim(v, "*")
		if isAllStars(v) {
			// All-'*' value: field exists / has any value.
			out = append(out, canonCond{Field: lc(field), Op: "exists_true", Negated: negated})
			continue
		}
		pre := strings.HasPrefix(v, "*")
		suf := strings.HasSuffix(v, "*")
		op := "eq"
		switch {
		case pre && suf && len(v) > 1:
			op = "contains"
		case pre:
			op = "endswith"
		case suf:
			op = "startswith"
		}
		body = stripEdgeWildcards(op, v)
		out = append(out, canonCond{Field: lc(field), Op: op, Value: canonValue(body, ci), Negated: negated, CI: ci})
	}
	return out
}

func valueCanon(field, op string, values []string, negated, ci bool) []canonCond {
	// Case-sensitivity only matters for text matches; force it off elsewhere
	// (cidrmatch, numeric comparisons) so the two dialects agree.
	if !textOps[op] {
		ci = false
	}
	var out []canonCond
	for _, v := range values {
		// A text match whose value is only '*' wildcards means "field exists".
		// A '?' is a real single-character match, so it is NOT existence.
		if textOps[op] && isAllStars(v) {
			out = append(out, canonCond{Field: lc(field), Op: "exists_true", Negated: negated})
			continue
		}
		v = stripEdgeWildcards(op, v)
		out = append(out, canonCond{Field: lc(field), Op: op, Value: canonValue(v, ci), Negated: negated, CI: ci})
	}
	return out
}

// compareCanon returns conditions in want missing from got, and vice versa.
func compareCanon(want, got []canonCond) (missing, extra []canonCond) {
	return diffCanon(want, got), diffCanon(got, want)
}

func diffCanon(a, b []canonCond) []canonCond {
	set := map[string]bool{}
	for _, c := range b {
		set[c.key()] = true
	}
	var out []canonCond
	seen := map[string]bool{}
	for _, c := range a {
		if !set[c.key()] && !seen[c.key()] {
			seen[c.key()] = true
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func lc(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// canonValue normalizes a match value so the two dialects compare equal:
// backslashes collapsed (Sigma vs EQL escaping), typographic dashes folded to
// ASCII (EQL's NormalizeQuery rewrites them), and case folded for
// case-insensitive matches.
func canonValue(v string, ci bool) string {
	v = strings.ReplaceAll(v, "\\\\", "\\")
	v = typographicFolder.Replace(v)
	if ci {
		v = strings.ToLower(v)
	}
	return v
}

// typographicFolder maps the characters EQL's NormalizeQuery rewrites back to
// their ASCII forms, so a Sigma value and its EQL round-trip compare equal.
var typographicFolder = strings.NewReplacer(
	"–", "-", "—", "-", "−", "-", // en/em dash, minus sign
	"’", "'", "‘", "'", "‚", "'", "‛", "'", // curly single quotes
	"“", `"`, "”", `"`, "„", `"`, "‟", `"`, // curly double quotes
)

// isAllStars reports whether a value consists only of '*' characters (one or
// more), i.e. a wildcard that matches any non-null value — Sigma/EQL existence.
func isAllStars(v string) bool {
	if v == "" {
		return false
	}
	return strings.Trim(v, "*") == ""
}

// hasEscapedWildcard reports whether a Sigma value contains an escaped
// wildcard (`\*` or `\?`), i.e. a literal star/question mark. EQL's wildcard
// operators (`:`/`like`) have no escape for these, so such a value cannot be
// faithfully round-tripped and is classified untranslatable rather than
// silently mismatched.
func hasEscapedWildcard(sig *sigma.ParseResult) bool {
	for _, c := range sig.Conditions {
		vals := c.Alternatives
		if len(vals) == 0 {
			vals = []string{c.Value}
		}
		for _, v := range vals {
			if strings.Contains(v, `\*`) || strings.Contains(v, `\?`) {
				return true
			}
		}
	}
	return false
}

// normalizationFragile are runes that EQL's NormalizeQuery folds to a space or
// strips entirely (non-breaking spaces and zero-width characters). A rule
// whose match value hinges on one cannot survive EQL's normalization.
var normalizationFragile = map[rune]bool{
	0x00A0: true, 0x202F: true, 0x2007: true, // non-breaking spaces
	0x200B: true, 0x200C: true, 0x200D: true, // zero-width space/joiners
	0xFEFF: true, 0x2060: true, // BOM, word joiner
}

// hasNormalizationFragileValue reports whether any Sigma match value contains a
// character EQL's NormalizeQuery would fold or drop.
func hasNormalizationFragileValue(sig *sigma.ParseResult) bool {
	for _, c := range sig.Conditions {
		vals := c.Alternatives
		if len(vals) == 0 {
			vals = []string{c.Value}
		}
		for _, v := range vals {
			for _, r := range v {
				if normalizationFragile[r] {
					return true
				}
			}
		}
	}
	return false
}

func eqlFieldName(f string) string {
	// EQL field names may contain dots already; a Sigma field with spaces or
	// hyphens needs backticks to be a valid EQL identifier path.
	if f == "" {
		return f
	}
	needsQuote := false
	for i := 0; i < len(f); i++ {
		ch := f[i]
		if ch == '_' || ch == '.' || (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			continue
		}
		needsQuote = true
		break
	}
	if needsQuote {
		return "`" + strings.ReplaceAll(f, "`", "``") + "`"
	}
	return f
}

func anyWildcard(values []string) bool {
	for _, v := range values {
		if strings.ContainsAny(v, "*?") {
			return true
		}
	}
	return false
}

func quoteList(values []string) string {
	if len(values) == 1 {
		return eqlQuote(values[0])
	}
	return "(" + strings.Join(quoteEach(values), ", ") + ")"
}

func quoteEach(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = eqlQuote(v)
	}
	return out
}

// eqlQuote renders a string as an EQL double-quoted literal. Unlike
// strconv.Quote it writes non-ASCII runes literally (EQL has no \uXXXX escape;
// its unicode escape is \u{...}), so values survive re-parsing intact.
func eqlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u{%x}`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func numLiteral(v string) string {
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v
	}
	return strconv.Quote(v)
}

func isTrue(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "true") }

func listOrScalar(values []string) any {
	if len(values) == 1 {
		return values[0]
	}
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// sortedCanon returns a stable, deduped rendering for reporting.
func sortedCanon(cs []canonCond) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.key()] {
			continue
		}
		seen[c.key()] = true
		out = append(out, c.key())
	}
	sort.Strings(out)
	return out
}
