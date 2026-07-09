# EQL Parser Design

Date: 2026-07-09
Status: Approved (built autonomously from explicit user request: "build an elasticsearch parser
in Go comparable to the level we have for the others. Build it wide, deep and TEST it immensely")

## Why EQL

The platform's Elastic integration is EQL-shaped end to end: `detection.go` maps
`"elastic", "eql"` → "Elastic EQL", threat-feed maps rule platform `"eql"` → `"elastic"`, and
`ExtractConditionsFromPipelineWithParser` currently returns nil for Elastic because no parser
exists. Elastic Security detection rules (the content this product ingests, generates, and
tests) use EQL for correlation rules. Kibana KQL / Lucene query strings are a separate, much
smaller language and can be a later module if needed.

## Prior art surveyed (2026-07-09)

- **No Go implementation of security EQL exists** (searched GitHub / pkg.go.dev). The Go
  packages named "eql" are unrelated languages (EliasDB graph QL, spreadsheet DSL) or the
  Elastic Agent condition mini-language (Elastic License).
- **Endgame `eql` (Python)** — reference implementation. AGPL; used to *verify behavior only*,
  no code derived from it.
- **Elasticsearch `EqlBase.g4`** — authoritative modern grammar. Elastic License 2.0; used to
  *learn the grammar shape* (productions, precedence, token rules), implementation is original.
- **Official EQL syntax docs** (elastic.co) — the primary spec this parser is derived from.

## Decisions

1. **Standalone module** `github.com/craftedsignal/eql-parser`, package `eql`, MIT, zero
   dependencies — mirrors kql-parser/leql-parser/spl-parser (module layout, extractor API,
   test-suite shape) and sigma-parser (hand-written parsing).
2. **Hand-written lexer + recursive-descent parser** (not ANTLR). ANTLR toolchain (Java) is
   unavailable, generated Go is 10-40x the code volume, and EQL is compact enough that a
   hand-written parser gives better error messages, recovery, and robustness control.
   sigma-parser already establishes this pattern in the fleet.
3. **Canonical dialect: modern Elasticsearch EQL** (sequences with maxspan/runs/until/missing
   events, samples, head/tail pipes, `:` / `like` / `regex` / `in` with `~` variants, optional
   `?fields`, backtick fields, `"""raw"""` strings, `\u{...}` escapes).
4. **Permissive acceptance of legacy Endgame EQL**, flagged but parsed: single-quoted strings,
   `?"raw"` strings, legacy pipes (`count`, `filter`, `sort`, `unique`, `unique_count`),
   lineage (`child of`, `descendant of`, `event of`), `=` as equality, `wildcard()`/`match()`
   functions. Real-world corpora mix both dialects; a parser that rejects half the wild
   content is useless for extraction.
5. **Extractor API mirrors kql-parser** so the platform adapter is mechanical:
   `ExtractConditions(query) *ParseResult`, `Condition{Field, Operator, Value, Negated,
   LogicalOp, Alternatives, ...}` plus EQL-native context (`EventCategory`, `SequenceStep`,
   `PipeStage`, `CaseInsensitive`, `Function`, `IsOptional`). Sequences/joins/samples surface
   as `SequenceInfo` (the EQL analog of kql `JoinInfo`), pipes as `PipeInfo`.
6. **Safety rails**: `MaxParseTime` (5s) watchdog, panic recovery at the API boundary,
   expression depth cap, input size cap — parser must never crash or hang on hostile input.
7. **AST is public** (`Parse(query) (*Query, error)`) with `String()` round-trip rendering,
   for future reverse-translation (sigma) work.

## Test plan ("immensely")

- `lexer_test.go` — every token form, escapes, unicode, unterminated forms.
- `parser_test.go` — grammar coverage, precedence, error cases, **round-trip property**
  (parse → render → parse → same AST).
- `extractor_test.go` — table-driven semantic extraction, negation/De Morgan, alternatives,
  step/pipe attribution.
- `realworld_test.go` — 150+ genuine queries (Elastic docs, detection-rule patterns,
  legacy Endgame style) with assertions.
- `corpus_test.go` + `testdata/corpus.json` — success/partial/failed/panic accounting with
  minimum parse-rate gate.
- `fuzz_test.go` — native Go fuzzing, invariants: no panic, non-nil result, well-formed
  conditions, bounded time.
- `stress_test.go` — 100k random structured queries + adversarial mutations (truncations,
  byte flips, quote/bracket surgery) of the corpus.
- `cmd/generated-corpus` + `generated_corpus_test.go` — bounded-AST differential testing:
  generate random ASTs with known expected conditions, render, extract, compare.
- Benchmarks for lexer/parser/extractor.
