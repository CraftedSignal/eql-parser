# EQL Parser

[![Go Reference](https://pkg.go.dev/badge/github.com/craftedsignal/eql-parser.svg)](https://pkg.go.dev/github.com/craftedsignal/eql-parser)
[![Go Report Card](https://goreportcard.com/badge/github.com/craftedsignal/eql-parser)](https://goreportcard.com/report/github.com/craftedsignal/eql-parser)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)

A production-ready Go parser for **EQL (Event Query Language)**, the correlation
language used by Elastic Security detection rules. It extracts conditions,
fields, correlation structure, and pipes from EQL queries used in Elasticsearch,
Kibana, and the legacy Endgame ecosystem.

Hand-written lexer and recursive-descent parser, **zero dependencies**, and
tested against a large corpus of real-world detection rules plus millions of
fuzzed and mutated inputs.

## Features

- **Full EQL grammar**: event queries, `sequence`, `join`, and `sample`
  correlations, `by` join keys, `with maxspan`, `with runs`, `until`, and
  `![...]` missing-event clauses.
- **Every operator**: `==` `!=` `<` `<=` `>` `>=`, the case-insensitive `:`,
  `like` / `like~`, `regex` / `regex~`, `in` / `in~` / `not in`, arithmetic,
  and boolean `and` / `or` / `not`.
- **Condition extraction**: field, operator, value, negation (De Morgan-aware),
  case-sensitivity, alternatives for list operators, event category, sequence
  step, and pipe stage.
- **Field discovery**: optional `?fields`, backtick-escaped `` `field names` ``,
  dotted paths, and array indices (`process.args[0]`).
- **Functions**: `wildcard`, `cidrMatch`, `endsWith`, `startsWith`,
  `stringContains`, `length`, `match`, and the `~` case-insensitive variants.
- **Pipes**: `head`, `tail`, plus the legacy `count`, `unique`, `unique_count`,
  `sort`, and `filter`.
- **Permissive legacy acceptance**: single-quoted strings, `=` equality,
  `child of` / `descendant of` / `event of` lineage, and `?"raw"` strings from
  Endgame-era rules.
- **Bounded processing**: synchronous parsing with per-call input, token, node,
  work, condition, output, and nesting limits, cooperative deadlines, and recovery that still
  extracts what it can.
- **Input normalization**: smart quotes, zero-width characters, markdown code
  fences, and JSON-escaped newlines from copy-pasted or extracted rules.

## Installation

```bash
go get github.com/craftedsignal/eql-parser
```

## Usage

### Condition extraction

```go
package main

import (
	"fmt"

	eql "github.com/craftedsignal/eql-parser"
)

func main() {
	result := eql.ExtractConditions(
		`process where process.name : "cmd.exe" and process.parent.name : "explorer.exe"`,
	)

	for _, c := range result.Conditions {
		fmt.Printf("%s %s %q (category=%s)\n", c.Field, c.Operator, c.Value, c.EventCategory)
	}
	// process.name : "cmd.exe" (category=process)
	// process.parent.name : "explorer.exe" (category=process)
}
```

### Correlation (sequence / join / sample)

```go
result := eql.ExtractConditions(`
sequence by process.entity_id with maxspan=5m
  [process where event.type == "start" and process.name : "msxsl.exe"]
  [network where event.type == "connection" and network.direction : "egress"]`)

seq := result.Sequence
fmt.Println(seq.Kind)        // "sequence"
fmt.Println(seq.MaxSpanMS)   // 300000
fmt.Println(seq.ByFields)    // [process.entity_id]
fmt.Println(len(seq.Steps))  // 2
```

### AST access

```go
q, err := eql.Parse(`process where process.name in ("a.exe", "b.exe")`)
if err != nil {
	// err is non-nil for malformed input, but q still holds the partial AST.
}
fmt.Println(q.String()) // canonical re-rendering (round-trips)
```

## API

| Function | Description |
|----------|-------------|
| `ExtractConditions(query) *ParseResult` | Full extraction: conditions, sequence info, pipes, fields, errors. Never panics. |
| `ExtractConditionsContext(ctx, query) *ParseResult` | Extraction with caller cancellation and a cooperative deadline. |
| `AnalyzeQueryContext(ctx, query) *SemanticQuery` | Semantic analysis under the same extraction/conversion budget. |
| `ParseContext(ctx, query) (*Query, error)` | AST parsing with cancellation and resource bounds. |
| `ParseExpressionContext(ctx, input) (Expr, error)` | Standalone expression parsing with cancellation and resource bounds. |
| `Parse(query) (*Query, error)` | Parse to an AST with round-trippable `String()`. |
| `ParseExpression(input) (Expr, error)` | Parse a standalone boolean expression. |
| `NormalizeQuery(query) string` | Clean up pasted/extracted query text. |
| `ClassifyFieldUsage(result, field) FieldUsage` | Structured field role: join key, sequence steps, pipes, until. |
| `ClassifyFieldProvenance(result, field) FieldProvenance` | Field role as a `main`/`joined`/`join_key`/`ambiguous` string (sibling-parser vocabulary). |
| `DeduplicateConditions(conditions) []Condition` | Remove exact-duplicate conditions, preserving context. |
| `IsStatisticalQuery(result) bool` | Query aggregates/correlates (count/unique pipe, or sequence/sample). |
| `HasComplexWhereConditions(result) bool` | Any pattern/list/function operator is used. |
| `GetEventTypeFromConditions(result) string` | Canonical event type (e.g. `windows_4688`) from an event-code condition. |
| `MaxSpanDuration(span) (int64, bool)` | Convert a `maxspan` value to milliseconds. |

`ExtractConditions` and `AnalyzeQuery` use `MaxParseTime` (default 5 seconds)
as a cooperative deadline. No parsing goroutine continues after they return.
Cancellation is checked during lexing, parsing, extraction, and semantic
conversion, and before/after bounded normalization and expression rendering;
these checkpoints do not provide hard wall-clock preemption.

The 1 MiB input limit is supplemented by per-call limits of 32,768 tokens,
32,768 AST/expression nodes, 4,096 emitted conditions (including step
subqueries), 4 MiB of accounted output text/structure, and 4,194,304 work
units. Extraction and semantic conversion share these budgets. Resource or
cancellation failure returns an error with no partial conditions or semantic
tree. Ordinary syntax errors still support best-effort extraction.

`ParseResult` carries `Conditions`, `EventCategories`, `Sequence`, `Pipes`,
`Commands`, `Fields`, `JoinKeys` (also via `GroupByFields()`), and `Errors`.
`Conditions` is a flat, best-effort field projection. For event filters, use
`Expression` as the complete boolean tree: a `condition` leaf refers to a
condition index, while a `scalar` leaf carries canonical EQL syntax and any
enclosing NOT parity. A scalar can represent a constant, a field-free
predicate, or a computed/function predicate. Consumers must preserve it or
explicitly reject it; dropping it or treating it as true changes the filter.
The corresponding `SemanticQuery.Expression` carries owned condition and
scalar leaves. Scalar syntax is canonical rendering, not the original source
substring or an evaluated truth value.
Each `Condition` records the `Field`, `Operator`, `Value`, `Negated`,
`CaseInsensitive`, `LogicalOp`, `Alternatives`, `EventCategory`,
`SequenceStep`, `FromUntil`, `FromMissing`, `PipeStage`, `IsOptional`,
`Function`, `ValueIsField`, `CoFields`, and `Lineage`. Sequence/join/sample
steps additionally expose a recursive `Subquery *ParseResult` — a
self-contained extraction of that correlation term, the EQL analog of a join
subsearch.

Newly extracted field conditions also carry `RHS` and `PredicateSyntax`.
`RHS` identifies string, number, bool, null, field, list, or expression
operands; list items retain their kinds, order, and repetitions. Number text
keeps its original notation, and a null operand has no sentinel text.
`PredicateSyntax` is the full atomic predicate in canonical EQL, including
function arguments and their order. For a safely merged OR condition, it is
the canonical OR of its source atoms. Computed predicates remain scalar leaves
in the complete expression even when flat extraction reports a field.
Older serialized conditions without `RHS` are untyped: `Value` alone does not
establish whether `null`, `true`, or `1` was a literal or a string. These
metadata fields are additive in JSON, but adding exported Go struct fields
can break unkeyed struct literals. Consumers should use `Expression` for
boolean meaning and either support scalar leaves and typed RHS explicitly or
reject unsupported predicates.

Parsing is intentionally permissive: field-to-field comparisons such as
`x == y` can be represented locally, although modern Elasticsearch EQL does
not support them. Parser acceptance alone does not establish service support.

## Sigma interoperability

A bidirectional Sigma ↔ EQL translator ships in `cmd/generated-corpus` (kept
out of the dependency-free core, matching the sibling parsers). Correctness is
enforced by a round-trip corpus: translating a rule to the other dialect and
re-parsing it must reproduce the same set of conditions under a shared
canonical vocabulary. Against the SigmaHQ corpus (3,100+ rules) and the real
EQL corpus, **every field-translatable rule round-trips with zero condition
loss in both directions** — constructs with no faithful cross-dialect form
(EQL sequences, Sigma free-text keywords, escaped wildcards, unicode-obfuscation
values) are explicitly classified, never silently mistranslated. Regenerate
with `make sigma-corpus`.

At larger scale, a **100,000-rule corpus** — ~46k real Sigma rules scraped from
68 GitHub repositories plus the CraftedSignal library, deduped, combined with
generated complex rules — runs through parse → translate → round-trip with
**zero panics and 100% round-trip fidelity** on every field-translatable rule.
The translated EQL (~94k queries) is committed and re-parsed by the core test
suite. Build with `make rules-corpus`.

## Testing

```bash
make test        # full suite with coverage, then short suite with race detection
make fuzz        # native Go fuzzing (FuzzExtractConditions, FuzzParse)
make corpus      # regenerate the differential test corpus
make benchmark   # lexer / parser / extractor benchmarks
```

CI and `make test` run `go test -v -count=1 -timeout=10m -coverprofile=coverage.out ./...`
followed by `go test -short -race -count=1 -timeout=5m ./...`. The full run
retains every corpus and stress test and produces the coverage report. The
short race run includes a bounded concurrent test of parsing, extraction,
semantic analysis, and cancellation/resource-failure isolation. Separating the
runs avoids the combined cost of race detection and atomic coverage on the
large corpora. CI checks out the corpus files with Git LFS and uploads
`coverage.out` to Codecov.

The suite includes unit tests for the lexer, parser, and extractor; a
round-trip property test (`parse → render → parse` is a fixpoint); the full
**1,209 real detection rules** (elastic/detection-rules + endgameinc/eqllib),
all parsing cleanly; a curated valid/adversarial corpus; bounded-AST
differential testing with an independent oracle; a **500,000-query** generated
corpus (0 panics, 100% parse rate at scale); a Sigma ↔ EQL round-trip corpus;
native fuzzing (millions of executions); and a 100k-iteration structured stress
test plus ~200k adversarial mutation runs — all with zero panics, at **99.9%
statement coverage**.

## Dialect notes

The canonical dialect is **modern Elasticsearch EQL**. Legacy Endgame syntax is
accepted permissively (and flagged where it is invalid in modern EQL) because
real-world rule corpora mix both. The parser reports semantic problems
(`sequence` needing two events, missing-event clauses needing `maxspan`,
comparison chaining, unknown pipes) in `ParseResult.Errors` while still
extracting everything it can — extraction is best-effort by design.

## Limitations

- EQL functions are recognized structurally; their return types are not
  evaluated. `length(x) > 2` extracts `x` with the `length` function recorded,
  not a computed value.
- Regex and wildcard values are extracted as literal strings; patterns are not
  compiled or matched.
- The parser targets extraction and structural analysis, not execution. It does
  not query Elasticsearch.

## License

AGPL-3.0 — see [LICENSE](LICENSE).
