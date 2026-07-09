package eql

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MaxParseTime is the maximum time allowed for parsing a single query.
// Queries that exceed it return an error result instead of hanging.
var MaxParseTime = 5 * time.Second

// Condition is one field predicate extracted from an EQL query.
type Condition struct {
	Field           string   `json:"field"`
	Operator        string   `json:"operator"` // "==", "!=", "<", "<=", ">", ">=", ":", "like", "regex", "in", or a function name ("wildcard", "cidrmatch", ...)
	Value           string   `json:"value"`
	Negated         bool     `json:"negated"`
	CaseInsensitive bool     `json:"case_insensitive,omitempty"` // : operator, ~ variants
	LogicalOp       string   `json:"logical_op"`                 // "AND" or "OR" connecting to the previous condition
	Alternatives    []string `json:"alternatives,omitempty"`     // all values for list operators and merged same-field ORs
	EventCategory   string   `json:"event_category,omitempty"`   // category of the event filter this came from ("any" or "" for bare)
	SequenceStep    int      `json:"sequence_step"`              // 0-based step index; 0 for non-sequence queries
	FromUntil       bool     `json:"from_until,omitempty"`       // condition came from an until clause
	FromMissing     bool     `json:"from_missing,omitempty"`     // condition came from a ![ missing-event clause
	PipeStage       int      `json:"pipe_stage"`                 // 0 = main query, N = Nth pipe (1-based, filter pipes)
	IsOptional      bool     `json:"is_optional,omitempty"`      // field carried the ? optional marker
	Function        string   `json:"function,omitempty"`         // function wrapping the field, e.g. "length" in length(f) > 5
	ValueIsField    bool     `json:"value_is_field,omitempty"`   // Value names another field (field-to-field comparison)
	CoFields        []string `json:"co_fields,omitempty"`        // other fields in the same comparison (e.g. b in "a + b > 10")
	Lineage         string   `json:"lineage,omitempty"`          // "child", "descendant", "event" for legacy lineage subqueries
}

// SequenceStepInfo describes one bracketed event of a sequence/join/sample.
// Subquery holds a self-contained recursive extraction of the step's event
// filter (`[category where ...]`), the EQL analog of a join subsearch — its
// conditions are step-relative (SequenceStep 0) and independently inspectable.
type SequenceStepInfo struct {
	EventCategory  string       `json:"event_category"`
	ByFields       []string     `json:"by_fields,omitempty"`
	Runs           int          `json:"runs,omitempty"`
	Missing        bool         `json:"missing,omitempty"`
	ConditionCount int          `json:"condition_count"`
	Subquery       *ParseResult `json:"subquery,omitempty"`
}

// SequenceInfo describes the correlation structure of a sequence, join, or
// sample query — the EQL analog of join metadata in the other parsers.
type SequenceInfo struct {
	Kind      string             `json:"kind"` // "sequence", "join", "sample"
	MaxSpan   string             `json:"max_span,omitempty"`
	MaxSpanMS int64              `json:"max_span_ms,omitempty"`
	ByFields  []string           `json:"by_fields,omitempty"` // global join keys
	Steps     []SequenceStepInfo `json:"steps"`
	Until     *SequenceStepInfo  `json:"until,omitempty"`
}

// PipeInfo is one post-processing pipe with rendered arguments.
type PipeInfo struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// ParseResult contains everything extracted from a query.
type ParseResult struct {
	Conditions      []Condition   `json:"conditions"`
	EventCategories []string      `json:"event_categories,omitempty"` // unique categories in source order
	Sequence        *SequenceInfo `json:"sequence,omitempty"`         // set for sequence/join/sample queries
	Pipes           []PipeInfo    `json:"pipes,omitempty"`
	Commands        []string      `json:"commands,omitempty"`  // construct kind + pipe names, in order
	Fields          []string      `json:"fields,omitempty"`    // every field referenced anywhere (source order, deduped)
	JoinKeys        []string      `json:"join_keys,omitempty"` // every by-clause field (global + per-step)
	Errors          []string      `json:"errors,omitempty"`
}

// FieldUsage classifies how a field participates in a query.
type FieldUsage struct {
	IsJoinKey bool  `json:"is_join_key"`
	Steps     []int `json:"steps,omitempty"` // sequence steps referencing the field
	InPipes   bool  `json:"in_pipes"`
	InUntil   bool  `json:"in_until"`
}

// extractHook, when non-nil, is invoked at the start of the extraction
// goroutine. It exists solely so tests can exercise the panic-recovery and
// timeout guards (the parser itself never panics or hangs on real input).
var extractHook func(string)

// ExtractConditions parses an EQL query and extracts conditions, correlation
// structure, pipes, and referenced fields. It never panics and never hangs:
// hostile input yields a result whose Errors explains what went wrong.
// Input is normalized with NormalizeQuery first.
func ExtractConditions(query string) *ParseResult {
	type outcome struct{ res *ParseResult }
	done := make(chan outcome, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{&ParseResult{
					Conditions: []Condition{},
					Errors:     []string{fmt.Sprintf("parser panic: %v", r)},
				}}
			}
		}()
		if extractHook != nil {
			extractHook(query)
		}
		done <- outcome{extractConditions(query)}
	}()

	select {
	case o := <-done:
		return o.res
	case <-time.After(MaxParseTime):
		return &ParseResult{
			Conditions: []Condition{},
			Errors:     []string{fmt.Sprintf("parser timeout: query took longer than %s to parse", MaxParseTime)},
		}
	}
}

func extractConditions(query string) *ParseResult {
	res := &ParseResult{Conditions: []Condition{}}

	if len(query) > MaxInputSize {
		res.Errors = []string{ErrInputTooLarge.Error()}
		return res
	}
	normalized := NormalizeQuery(query)
	if strings.TrimSpace(normalized) == "" {
		res.Errors = []string{ErrEmptyQuery.Error()}
		return res
	}

	ast, errs := parseTolerant(normalized)
	res.Errors = errs

	ex := newExtractor(res)
	ex.extractQuery(ast)
	res.Conditions = mergeOrAlternatives(res.Conditions)
	return res
}

// newExtractor builds a walker that accumulates into res.
func newExtractor(res *ParseResult) *extractor {
	return &extractor{
		res:       res,
		fieldSeen: map[string]struct{}{},
		joinSeen:  map[string]struct{}{},
		catSeen:   map[string]struct{}{},
	}
}

// extractSubquery runs a fresh, self-contained extraction over a single event
// filter, returning its own ParseResult. Conditions are step-relative
// (SequenceStep 0). Used to populate SequenceStepInfo.Subquery so consumers
// can recurse into correlation terms the way they would a join subsearch.
func extractSubquery(e *EventQuery) *ParseResult {
	sub := &ParseResult{Conditions: []Condition{}}
	if e == nil {
		return sub
	}
	ex := newExtractor(sub)
	ex.enterEventQuery(e, 0, false, false, "")
	sub.Conditions = mergeOrAlternatives(sub.Conditions)
	return sub
}

// extractor walks the AST accumulating results. Context fields describe
// where in the query the walk currently is.
type extractor struct {
	res *ParseResult

	category  string
	step      int
	fromUntil bool
	missing   bool
	pipeStage int
	lineage   string
	negated   bool
	logicalOp string // connective for the next emitted condition

	fieldSeen map[string]struct{}
	joinSeen  map[string]struct{}
	catSeen   map[string]struct{}
}

func (ex *extractor) extractQuery(q *Query) {
	if q == nil {
		return
	}
	switch body := q.Body.(type) {
	case *EventQuery:
		ex.enterEventQuery(body, 0, false, false, "")
	case *Sequence:
		ex.extractSequence(body)
	}

	for i, p := range q.Pipes {
		info := PipeInfo{Name: p.Name}
		for _, a := range p.Args {
			info.Args = append(info.Args, ExprString(a))
			ex.collectFields(a)
		}
		ex.res.Pipes = append(ex.res.Pipes, info)
		ex.res.Commands = append(ex.res.Commands, p.Name)

		if p.Name == "filter" && len(p.Args) > 0 {
			ex.pipeStage = i + 1
			ex.category = ""
			ex.step = 0
			ex.logicalOp = ""
			for _, a := range p.Args {
				ex.walkExpr(a)
			}
			ex.pipeStage = 0
		}
	}
}

func (ex *extractor) enterEventQuery(e *EventQuery, step int, fromUntil, missing bool, seedOp string) {
	if e == nil {
		return
	}
	cat := e.Category
	if e.CategoryAny {
		cat = "any"
	}
	if cat != "" {
		if _, ok := ex.catSeen[cat]; !ok {
			ex.catSeen[cat] = struct{}{}
			ex.res.EventCategories = append(ex.res.EventCategories, cat)
		}
	}
	prevCat, prevStep, prevUntil, prevMissing := ex.category, ex.step, ex.fromUntil, ex.missing
	ex.category, ex.step, ex.fromUntil, ex.missing = cat, step, fromUntil, missing
	ex.logicalOp = seedOp
	if e.Where != nil {
		ex.collectFields(e.Where)
		ex.walkExpr(e.Where)
	}
	ex.category, ex.step, ex.fromUntil, ex.missing = prevCat, prevStep, prevUntil, prevMissing
}

func (ex *extractor) extractSequence(s *Sequence) {
	if s == nil {
		return
	}
	info := &SequenceInfo{Kind: string(s.Kind), MaxSpan: s.MaxSpan}
	if ms, ok := MaxSpanDuration(s.MaxSpan); ok {
		info.MaxSpanMS = ms
	}
	ex.res.Commands = append(ex.res.Commands, string(s.Kind))

	for _, by := range s.By {
		info.ByFields = append(info.ByFields, ex.registerJoinKey(by)...)
	}

	for i, st := range s.Steps {
		stepInfo := SequenceStepInfo{Missing: st.Missing, Runs: st.Runs}
		if st.Query != nil {
			stepInfo.EventCategory = st.Query.Category
			if st.Query.CategoryAny {
				stepInfo.EventCategory = "any"
			}
		}
		for _, by := range st.By {
			stepInfo.ByFields = append(stepInfo.ByFields, ex.registerJoinKey(by)...)
		}
		before := len(ex.res.Conditions)
		if st.Query != nil {
			ex.enterEventQuery(st.Query, i, false, st.Missing, "")
			stepInfo.Subquery = extractSubquery(st.Query)
		}
		stepInfo.ConditionCount = len(ex.res.Conditions) - before
		info.Steps = append(info.Steps, stepInfo)
	}

	if s.Until != nil {
		untilInfo := SequenceStepInfo{Missing: s.Until.Missing}
		if s.Until.Query != nil {
			untilInfo.EventCategory = s.Until.Query.Category
			if s.Until.Query.CategoryAny {
				untilInfo.EventCategory = "any"
			}
		}
		for _, by := range s.Until.By {
			untilInfo.ByFields = append(untilInfo.ByFields, ex.registerJoinKey(by)...)
		}
		before := len(ex.res.Conditions)
		if s.Until.Query != nil {
			ex.enterEventQuery(s.Until.Query, len(s.Steps), true, false, "")
			untilInfo.Subquery = extractSubquery(s.Until.Query)
		}
		untilInfo.ConditionCount = len(ex.res.Conditions) - before
		info.Until = &untilInfo
	}

	ex.res.Sequence = info
}

// registerJoinKey records the fields of a by-clause expression and returns
// their names (or the rendered expression when it contains no field).
func (ex *extractor) registerJoinKey(e Expr) []string {
	fields := collectFieldNodes(e)
	if len(fields) == 0 {
		txt := ExprString(e)
		if txt == "" {
			return nil
		}
		return []string{txt}
	}
	var names []string
	for _, f := range fields {
		name := f.Name()
		names = append(names, name)
		ex.addField(f)
		if _, ok := ex.joinSeen[name]; !ok {
			ex.joinSeen[name] = struct{}{}
			ex.res.JoinKeys = append(ex.res.JoinKeys, name)
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// Expression walking
// ---------------------------------------------------------------------------

func (ex *extractor) walkExpr(e Expr) {
	switch v := e.(type) {
	case *Binary:
		switch v.Op {
		case "and", "or":
			op := strings.ToUpper(v.Op)
			if ex.negated {
				// De Morgan: not (a or b) == not a and not b.
				if op == "OR" {
					op = "AND"
				} else {
					op = "OR"
				}
			}
			ex.walkExpr(v.L)
			ex.logicalOp = op
			ex.walkExpr(v.R)
		case "==", "!=", "<", "<=", ">", ">=":
			ex.emitComparison(v.Op, v.L, v.R)
		default:
			// Arithmetic in boolean position: no condition to extract.
		}

	case *Not:
		ex.negated = !ex.negated
		ex.walkExpr(v.X)
		ex.negated = !ex.negated

	case *Paren:
		ex.walkExpr(v.X)

	case *Unary:
		ex.walkExpr(v.X)

	case *InExpr:
		ex.emitList("in", v.X, v.List, v.Negated, v.Insensitive)

	case *PatternExpr:
		insensitive := v.Insensitive || v.Kind == PatternSeq
		ex.emitList(string(v.Kind), v.X, v.Patterns, false, insensitive)

	case *Call:
		ex.emitCall(v)

	case *Field:
		// Bare boolean field: `process where process.interactive`.
		ex.emit(Condition{
			Field:      v.Name(),
			Operator:   "==",
			Value:      "true",
			IsOptional: v.Optional,
		})

	case *Lineage:
		prev := ex.lineage
		ex.lineage = v.Kind
		ex.enterEventQuery(v.Sub, ex.step, ex.fromUntil, ex.missing, ex.logicalOp)
		ex.lineage = prev

	case *Literal, *BadExpr, nil:
		// Nothing to extract.
	}
}

// emitComparison handles ==, !=, <, <=, >, >= with a field on either side.
// It records the primary field, and any other fields participating in the
// same comparison (arithmetic operands, extra function arguments) as CoFields
// so nothing is silently dropped from the condition-level view.
func (ex *extractor) emitComparison(op string, l, r Expr) {
	lf, lfn := primaryField(l)
	rf, rfn := primaryField(r)

	switch {
	case lf != nil && rf != nil:
		ex.emit(Condition{
			Field:        lf.Name(),
			Operator:     op,
			Value:        rf.Name(),
			ValueIsField: true,
			IsOptional:   lf.Optional,
			Function:     lfn,
			CoFields:     coFields(l, r, lf.Name(), rf.Name()),
		})
	case lf != nil:
		ex.emit(Condition{
			Field:      lf.Name(),
			Operator:   op,
			Value:      valueText(r),
			IsOptional: lf.Optional,
			Function:   lfn,
			CoFields:   coFields(l, r, lf.Name(), ""),
		})
	case rf != nil:
		ex.emit(Condition{
			Field:      rf.Name(),
			Operator:   mirrorOp(op),
			Value:      valueText(l),
			IsOptional: rf.Optional,
			Function:   rfn,
			CoFields:   coFields(l, r, rf.Name(), ""),
		})
	}
}

// coFields returns every field referenced across both sides of a comparison
// except the primary field and (when the RHS is itself a field) the value
// field, in source order with duplicates removed.
func coFields(l, r Expr, primary, value string) []string {
	nodes := append(collectFieldNodes(l), collectFieldNodes(r)...)
	var out []string
	seen := map[string]struct{}{primary: {}}
	if value != "" {
		seen[value] = struct{}{}
	}
	for _, f := range nodes {
		name := f.Name()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// emitList handles in/:/like/regex including their list forms.
func (ex *extractor) emitList(op string, x Expr, values []Expr, negated, insensitive bool) {
	f, fn := primaryField(x)
	if f == nil {
		return
	}
	var texts []string
	for _, v := range values {
		texts = append(texts, valueText(v))
	}
	cond := Condition{
		Field:           f.Name(),
		Operator:        op,
		Negated:         negated,
		CaseInsensitive: insensitive,
		IsOptional:      f.Optional,
		Function:        fn,
	}
	if len(texts) > 0 {
		cond.Value = texts[0]
	}
	if len(texts) > 1 {
		cond.Alternatives = texts
	}
	ex.emit(cond)
}

// booleanFunctions maps lowercase EQL predicate-style function names to
// their canonical operator spelling. The first field argument becomes the
// condition field; remaining literal arguments become values.
var booleanFunctions = map[string]string{
	"wildcard":       "wildcard",
	"match":          "match",
	"matchlite":      "matchLite",
	"cidrmatch":      "cidrMatch",
	"startswith":     "startsWith",
	"endswith":       "endsWith",
	"stringcontains": "stringContains",
	"between":        "between",
	"arraycontains":  "arrayContains",
	"arraysearch":    "arraySearch",
}

// emitCall extracts a condition from a function call in boolean position,
// e.g. wildcard(process.name, "*.exe", "*.dll").
func (ex *extractor) emitCall(c *Call) {
	canonical := canonicalFunction(c.Name)

	f, _ := primaryField(&Paren{X: argsAsExpr(c)})
	if f == nil {
		return
	}

	var literals []string
	for _, a := range c.Args {
		if lit, ok := unwrap(a).(*Literal); ok {
			literals = append(literals, literalText(lit))
		}
	}

	cond := Condition{
		Field:           f.Name(),
		Operator:        canonical,
		CaseInsensitive: c.Insensitive,
		IsOptional:      f.Optional,
		Function:        canonical,
	}
	if len(literals) > 0 {
		cond.Value = literals[0]
		if len(literals) > 1 {
			cond.Alternatives = literals
		}
	}
	ex.emit(cond)
}

// argsAsExpr chains call arguments so primaryField can scan them in order.
func argsAsExpr(c *Call) Expr {
	if len(c.Args) == 0 {
		return &Literal{Kind: LitNull}
	}
	e := c.Args[0]
	for _, a := range c.Args[1:] {
		e = &Binary{Op: "+", L: e, R: a}
	}
	return e
}

func (ex *extractor) emit(c Condition) {
	if c.Field == "" {
		return
	}
	if c.Operator == "" {
		c.Operator = "=="
	}
	c.Negated = c.Negated != ex.negated // XOR with walk-context negation
	c.EventCategory = ex.category
	c.SequenceStep = ex.step
	c.FromUntil = ex.fromUntil
	c.FromMissing = ex.missing
	c.PipeStage = ex.pipeStage
	c.LogicalOp = ex.logicalOp
	c.Lineage = ex.lineage
	ex.logicalOp = ""
	ex.res.Conditions = append(ex.res.Conditions, c)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// primaryField returns the first field referenced in an expression, along
// with the name of the innermost function wrapping it ("" when the field is
// used directly).
func primaryField(e Expr) (*Field, string) {
	switch v := e.(type) {
	case *Field:
		return v, ""
	case *Paren:
		return primaryField(v.X)
	case *Unary:
		return primaryField(v.X)
	case *Not:
		return primaryField(v.X)
	case *Binary:
		if f, fn := primaryField(v.L); f != nil {
			return f, fn
		}
		return primaryField(v.R)
	case *Call:
		for _, a := range v.Args {
			if f, fn := primaryField(a); f != nil {
				if fn == "" {
					fn = canonicalFunction(v.Name)
				}
				return f, fn
			}
		}
		return nil, ""
	case *InExpr:
		return primaryField(v.X)
	case *PatternExpr:
		return primaryField(v.X)
	}
	return nil, ""
}

func canonicalFunction(name string) string {
	if c, ok := booleanFunctions[strings.ToLower(name)]; ok {
		return c
	}
	return strings.ToLower(name)
}

// collectFieldNodes returns every Field node in an expression, in source
// order.
func collectFieldNodes(e Expr) []*Field {
	var out []*Field
	var walk func(Expr)
	walk = func(e Expr) {
		switch v := e.(type) {
		case *Field:
			out = append(out, v)
		case *Paren:
			walk(v.X)
		case *Unary:
			walk(v.X)
		case *Not:
			walk(v.X)
		case *Binary:
			walk(v.L)
			walk(v.R)
		case *Call:
			for _, a := range v.Args {
				walk(a)
			}
		case *InExpr:
			walk(v.X)
			for _, i := range v.List {
				walk(i)
			}
		case *PatternExpr:
			walk(v.X)
			for _, pat := range v.Patterns {
				walk(pat)
			}
		case *Lineage:
			if v.Sub != nil && v.Sub.Where != nil {
				walk(v.Sub.Where)
			}
		}
	}
	walk(e)
	return out
}

// collectFields registers every field of an expression into the result's
// Fields list (deduped, source order).
func (ex *extractor) collectFields(e Expr) {
	for _, f := range collectFieldNodes(e) {
		ex.addField(f)
	}
}

func (ex *extractor) addField(f *Field) {
	name := f.Name()
	if name == "" {
		return
	}
	if _, ok := ex.fieldSeen[name]; ok {
		return
	}
	ex.fieldSeen[name] = struct{}{}
	ex.res.Fields = append(ex.res.Fields, name)
}

// unwrap strips Paren and Unary(+) wrappers.
func unwrap(e Expr) Expr {
	for {
		switch v := e.(type) {
		case *Paren:
			e = v.X
		default:
			return e
		}
	}
}

// valueText renders the value side of a condition: decoded text for string
// literals, raw notation for numbers, canonical EQL for anything else.
func valueText(e Expr) string {
	switch v := unwrap(e).(type) {
	case *Literal:
		return literalText(v)
	case *Unary:
		if lit, ok := unwrap(v.X).(*Literal); ok && lit.Kind == LitNumber {
			return v.Op + lit.Text
		}
	}
	return ExprString(unwrap(e))
}

func literalText(l *Literal) string {
	switch l.Kind {
	case LitString:
		return l.Value
	case LitNumber:
		return l.Text
	case LitBool:
		if l.Bool {
			return "true"
		}
		return "false"
	default:
		return "null"
	}
}

// mirrorOp flips a comparison operator for value-op-field order.
func mirrorOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op // == and != are symmetric
	}
}

// mergeableOperators are operators for which `f OP a OR f OP b` is equivalent
// to a membership test over {a, b}, so OR-runs can fold into Alternatives.
// Ordering comparisons (<, <=, >, >=) are deliberately excluded: `f > a OR
// f > b` is not a membership test, and folding it would drop a bound.
var mergeableOperators = map[string]bool{
	"==": true, ":": true, "in": true, "like": true, "regex": true,
	"wildcard": true, "cidrMatch": true, "startsWith": true, "endsWith": true,
	"stringContains": true, "match": true,
}

// mergeOrAlternatives folds runs of OR-connected conditions on the same
// field/operator into a single condition with Alternatives, mirroring the
// other parsers' behavior for `f == "a" or f == "b"`.
func mergeOrAlternatives(conds []Condition) []Condition {
	if len(conds) < 2 {
		return conds
	}
	out := make([]Condition, 0, len(conds))
	for _, c := range conds {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if c.LogicalOp == "OR" &&
				mergeableOperators[c.Operator] &&
				c.Field == prev.Field &&
				c.Operator == prev.Operator &&
				c.Negated == prev.Negated &&
				c.CaseInsensitive == prev.CaseInsensitive &&
				c.Function == prev.Function &&
				c.EventCategory == prev.EventCategory &&
				c.SequenceStep == prev.SequenceStep &&
				c.PipeStage == prev.PipeStage &&
				c.FromUntil == prev.FromUntil &&
				c.Lineage == prev.Lineage &&
				!c.ValueIsField && !prev.ValueIsField {
				if len(prev.Alternatives) == 0 {
					prev.Alternatives = []string{prev.Value}
				}
				if len(c.Alternatives) > 0 {
					prev.Alternatives = append(prev.Alternatives, c.Alternatives...)
				} else {
					prev.Alternatives = append(prev.Alternatives, c.Value)
				}
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// FieldProvenance indicates where a field originates relative to a query's
// correlation structure. The string values match the sibling parsers'
// provenance vocabulary (main/joined/join_key/ambiguous) so a platform
// adapter can treat EQL uniformly with KQL/SPL; ProvenanceUnknown is an EQL
// addition for a field absent from the result.
type FieldProvenance string

const (
	// ProvenanceMain is a field filtering the primary event stream (a
	// non-sequence query, the first sequence step, or a filter pipe).
	ProvenanceMain FieldProvenance = "main"
	// ProvenanceJoined is a field from a correlated event — a later sequence
	// step or an until clause.
	ProvenanceJoined FieldProvenance = "joined"
	// ProvenanceJoinKey is a field used as a correlation key (a `by` clause).
	ProvenanceJoinKey FieldProvenance = "join_key"
	// ProvenanceAmbiguous is a field used in more than one distinct role.
	ProvenanceAmbiguous FieldProvenance = "ambiguous"
	// ProvenanceUnknown is returned for a field not present in the result.
	ProvenanceUnknown FieldProvenance = "unknown"
)

// ClassifyFieldProvenance reports the role a field plays in the parsed query,
// using the same vocabulary as the sibling parsers: a correlation key
// (join_key), a primary-stream filter (main), a correlated-event filter
// (joined), or several roles at once (ambiguous). A field the query never
// references is ProvenanceUnknown.
func ClassifyFieldProvenance(r *ParseResult, field string) FieldProvenance {
	if r == nil || field == "" {
		return ProvenanceUnknown
	}

	isJoinKey := false
	for _, k := range r.JoinKeys {
		if k == field {
			isJoinKey = true
			break
		}
	}

	inMain, inJoined := false, false
	present := isJoinKey
	for _, c := range r.Conditions {
		if c.Field != field && !(c.ValueIsField && c.Value == field) && !contains(c.CoFields, field) {
			continue
		}
		present = true
		// A later sequence step or an until clause is the "joined" side of
		// the correlation; step 0, non-sequence, and pipe filters are main.
		if c.SequenceStep > 0 || c.FromUntil {
			inJoined = true
		} else {
			inMain = true
		}
	}
	for _, p := range r.Pipes {
		for _, a := range p.Args {
			if fieldInText(a, field) {
				inMain = true
				present = true
			}
		}
	}

	roles := 0
	for _, b := range []bool{isJoinKey, inMain, inJoined} {
		if b {
			roles++
		}
	}
	switch {
	case !present:
		return ProvenanceUnknown
	case roles > 1:
		return ProvenanceAmbiguous
	case isJoinKey:
		return ProvenanceJoinKey
	case inJoined:
		return ProvenanceJoined
	default:
		return ProvenanceMain
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fieldInText reports whether a rendered pipe argument references field as a
// whole dotted path (not merely a substring of a larger identifier).
func fieldInText(arg, field string) bool {
	if field == "" {
		return false
	}
	from := 0
	for from <= len(arg)-len(field) {
		rel := strings.Index(arg[from:], field)
		if rel < 0 {
			return false
		}
		i := from + rel
		before := i == 0 || !isIdentByte(arg[i-1])
		after := i+len(field) >= len(arg) || !isIdentByte(arg[i+len(field)])
		if before && after {
			return true
		}
		from = i + 1
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '.' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// DeduplicateConditions removes duplicate conditions, keeping the first
// occurrence of each. Two conditions are duplicates when they agree on field,
// operator, value, negation, case-sensitivity, alternatives, and correlation
// context (step, pipe stage, until, lineage). Exported for parity with the
// sibling parsers' adapters.
func DeduplicateConditions(conds []Condition) []Condition {
	seen := make(map[string]struct{}, len(conds))
	out := make([]Condition, 0, len(conds))
	for _, c := range conds {
		key := conditionDedupKey(c)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

func conditionDedupKey(c Condition) string {
	var b strings.Builder
	b.WriteString(c.Field)
	b.WriteByte('\x00')
	b.WriteString(c.Operator)
	b.WriteByte('\x00')
	b.WriteString(c.Value)
	b.WriteByte('\x00')
	b.WriteString(strings.Join(c.Alternatives, "\x1f"))
	b.WriteByte('\x00')
	if c.Negated {
		b.WriteByte('n')
	}
	if c.CaseInsensitive {
		b.WriteByte('i')
	}
	b.WriteByte('\x00')
	b.WriteString(c.EventCategory)
	b.WriteByte('\x00')
	b.WriteString(c.Lineage)
	b.WriteByte('\x00')
	b.WriteString(strconv.Itoa(c.SequenceStep))
	b.WriteByte('\x00')
	b.WriteString(strconv.Itoa(c.PipeStage))
	if c.FromUntil {
		b.WriteByte('u')
	}
	return b.String()
}

// statisticalPipes are post-processing commands that aggregate or reduce the
// result set rather than filter it.
var statisticalPipes = map[string]bool{
	"count": true, "unique": true, "unique_count": true,
}

// IsStatisticalQuery reports whether the query aggregates or correlates rather
// than simply filtering: it uses a counting/uniquing pipe, or is a
// sequence/sample (stateful correlation). Mirrors the sibling helper.
func IsStatisticalQuery(r *ParseResult) bool {
	if r == nil {
		return false
	}
	for _, cmd := range r.Commands {
		if statisticalPipes[cmd] {
			return true
		}
	}
	if r.Sequence != nil && (r.Sequence.Kind == "sequence" || r.Sequence.Kind == "sample") {
		return true
	}
	return false
}

// complexOperators are matching operators whose semantics go beyond simple
// equality/ordering (wildcards, regex, CIDR, substring functions).
var complexOperators = map[string]bool{
	":": true, "like": true, "regex": true, "wildcard": true, "match": true,
	"matchLite": true, "cidrMatch": true, "between": true, "in": true,
	"startsWith": true, "endsWith": true, "stringContains": true,
}

// HasComplexWhereConditions reports whether any extracted condition uses a
// pattern/list/function operator rather than plain comparison. Mirrors the
// sibling helper.
func HasComplexWhereConditions(r *ParseResult) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Conditions {
		if complexOperators[c.Operator] || c.Function != "" || len(c.Alternatives) > 0 {
			return true
		}
	}
	return false
}

// HasUnmappedComputedFields reports whether the query defines computed fields
// that are not backed by a source field. EQL has no computed-field construct
// (no eval/extend/project), so this is always false; it exists for uniform
// API parity with the sibling parsers.
func HasUnmappedComputedFields(r *ParseResult) bool {
	return false
}

// eventCodeFields are the fields that carry a Windows/Sysmon event identifier.
var eventCodeFields = map[string]bool{
	"event.code": true, "event.id": true, "winlog.event_id": true,
	"winlog.event_data.eventid": true, "event_id": true, "eventid": true,
	"eventcode": true, "event_code": true,
}

// GetEventTypeFromConditions derives a canonical event-type string (e.g.
// "windows_4688", "sysmon_1") from an event-code condition, so EQL rules
// classify identically to the Sigma/SPL/KQL rules for the same telemetry.
// Returns "" when the query carries no recognizable event code.
func GetEventTypeFromConditions(r *ParseResult) string {
	if r == nil {
		return ""
	}
	code := ""
	sysmon := false
	for _, c := range r.Conditions {
		lf := strings.ToLower(c.Field)
		if eventCodeFields[lf] && code == "" {
			switch c.Operator {
			case "==", ":", "in", "=":
				if c.Value != "" {
					code = c.Value
				}
			}
		}
		if strings.Contains(strings.ToLower(c.Value), "sysmon") {
			sysmon = true
		}
		switch lf {
		case "event.provider", "winlog.provider_name", "provider", "event.module":
			if strings.Contains(strings.ToLower(c.Value), "sysmon") {
				sysmon = true
			}
		}
	}
	if code == "" {
		return ""
	}
	if sysmon {
		return "sysmon_" + code
	}
	return "windows_" + code
}

// GroupByFields returns the query's correlation keys (the `by` clause fields),
// under the name the sibling parsers use for their grouping axis. It is an
// alias for the JoinKeys field, provided for uniform adapter code.
func (r *ParseResult) GroupByFields() []string {
	if r == nil {
		return nil
	}
	return r.JoinKeys
}

// ClassifyFieldUsage reports how a field participates in the parsed query.
func ClassifyFieldUsage(r *ParseResult, field string) FieldUsage {
	var u FieldUsage
	for _, k := range r.JoinKeys {
		if k == field {
			u.IsJoinKey = true
			break
		}
	}
	stepSeen := map[int]struct{}{}
	for _, c := range r.Conditions {
		if c.Field != field && !(c.ValueIsField && c.Value == field) {
			continue
		}
		if c.PipeStage > 0 {
			u.InPipes = true
			continue
		}
		if c.FromUntil {
			u.InUntil = true
			continue
		}
		if _, ok := stepSeen[c.SequenceStep]; !ok {
			stepSeen[c.SequenceStep] = struct{}{}
			u.Steps = append(u.Steps, c.SequenceStep)
		}
	}
	for _, p := range r.Pipes {
		for _, a := range p.Args {
			if strings.Contains(a, field) {
				u.InPipes = true
			}
		}
	}
	return u
}
