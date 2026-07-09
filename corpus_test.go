package eql

import (
	"encoding/json"
	"os"
	"testing"
)

// QueryEntry is one query in the corpus file.
type QueryEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
	// Valid marks whether the query is expected to parse without errors.
	// Absent (false) means "unknown / adversarial"; such entries are only
	// checked for non-panic and well-formedness.
	Valid bool `json:"valid,omitempty"`
}

// TestCorpus runs the parser over a corpus of queries and reports a breakdown.
// Queries flagged Valid must parse cleanly; the rest must at least not panic
// and must produce well-formed conditions.
func TestCorpus(t *testing.T) {
	const corpusPath = "testdata/corpus.json"
	data, err := os.ReadFile(corpusPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("corpus not available at testdata/corpus.json")
		}
		t.Fatalf("read corpus: %v", err)
	}

	var queries []QueryEntry
	if err := json.Unmarshal(data, &queries); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	t.Logf("loaded %d queries", len(queries))

	var success, partial, noConditions, failed, panics int
	var validTotal, validClean int

	for _, q := range queries {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Errorf("PANIC on %q: %v", q.Name, r)
				}
			}()

			res := ExtractConditions(q.Query)

			for i, c := range res.Conditions {
				if c.Field == "" || c.Operator == "" {
					t.Errorf("%q condition %d malformed: %+v", q.Name, i, c)
				}
			}

			switch {
			case len(res.Conditions) > 0 && len(res.Errors) == 0:
				success++
			case len(res.Conditions) > 0:
				partial++
			case len(res.Errors) > 0:
				failed++
			default:
				noConditions++
			}

			if q.Valid {
				validTotal++
				if len(res.Errors) == 0 {
					validClean++
				} else {
					t.Errorf("query marked valid failed to parse: %q — %v", q.Name, res.Errors)
				}
			}
		}()
	}

	total := len(queries)
	t.Logf("results: success=%d partial=%d no_conditions=%d failed=%d panics=%d (total=%d)",
		success, partial, noConditions, failed, panics, total)

	// Correctness gate: every query marked valid must parse cleanly.
	// (Adversarial queries are expected to fail and are not counted here.)
	if validTotal > 0 {
		rate := float64(validClean) * 100 / float64(validTotal)
		t.Logf("valid-query clean-parse rate: %.1f%% (%d/%d)", rate, validClean, validTotal)
		if validClean != validTotal {
			t.Errorf("%d/%d valid queries failed to parse cleanly", validTotal-validClean, validTotal)
		}
	}
	if panics > 0 {
		t.Errorf("parser panicked %d times", panics)
	}
}

// TestGeneratedCorpus performs differential testing against a corpus produced
// by cmd/generated-corpus, where each query has a known set of expected
// conditions.
func TestGeneratedCorpus(t *testing.T) {
	const path = "testdata/generated/eql_generated_corpus.json"
	skipIfLFSPointer(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("generated corpus not available; run 'make corpus'")
		}
		t.Fatalf("read generated corpus: %v", err)
	}

	var cases []GeneratedCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse generated corpus: %v", err)
	}
	t.Logf("loaded %d generated cases", len(cases))

	var matched, mismatched int
	for _, gc := range cases {
		res := ExtractConditions(gc.Query)
		if diff := diffConditions(gc.Expected, res.Conditions); diff != "" {
			mismatched++
			if mismatched <= 20 {
				t.Errorf("case %d (%s): %s", gc.ID, gc.Query, diff)
			}
		} else {
			matched++
		}
	}
	t.Logf("generated: matched=%d mismatched=%d", matched, mismatched)
}
