package eql

import "testing"

// Regression tests for bugs surfaced by the real detection-rule corpus
// (testdata/real_eql_corpus.jsonl). Each corresponds to a query pattern that
// appears in production Elastic Security rules.

// A single-step sequence repeated with `with runs=N` (N>=2) is a valid
// multi-event sequence — the term must match N consecutive times. Found in
// ~14 real rules (nslookup DNS tunneling, pbpaste volume, Entra ID, SSH
// brute force, etc.).
func TestRegressionSingleStepSequenceWithRuns(t *testing.T) {
	valid := []string{
		`sequence by host.id with maxspan=5m [process where process.name : "nslookup.exe"] with runs=10`,
		`sequence by user.id with maxspan=10m [any where event.module == "azure"] with runs=2`,
		`sequence by host.id [process where process.name : "pbpaste"] with runs = 5`, // spaces around =
	}
	for _, q := range valid {
		res := ExtractConditions(q)
		if len(res.Errors) != 0 {
			t.Errorf("valid single-step runs sequence errored: %q → %v", q, res.Errors)
		}
		if res.Sequence == nil || len(res.Sequence.Steps) != 1 || res.Sequence.Steps[0].Runs < 2 {
			t.Errorf("unexpected sequence shape for %q: %+v", q, res.Sequence)
		}
	}

	// A single step WITHOUT runs (or runs=1) is still an error.
	for _, q := range []string{
		`sequence [process where process.name : "x"]`,
		`sequence [process where process.name : "x"] with runs=1`,
	} {
		res := ExtractConditions(q)
		if len(res.Errors) == 0 {
			t.Errorf("single-step sequence without runs>=2 should error: %q", q)
		}
	}
}

// Legacy Endgame field-list pipes accept space-separated fields, not only
// comma-separated. Found in the "Network Service Scanning via Port" rule:
// `| unique unique_pid destination_port | unique_count unique_pid`.
func TestRegressionSpaceSeparatedPipeFields(t *testing.T) {
	res := ExtractConditions(`network where subtype.incoming | unique unique_pid destination_port | unique_count unique_pid | filter count > 25`)
	if len(res.Errors) != 0 {
		t.Fatalf("space-separated pipe fields errored: %v", res.Errors)
	}
	if len(res.Pipes) != 3 {
		t.Fatalf("pipes = %+v", res.Pipes)
	}
	if len(res.Pipes[0].Args) != 2 {
		t.Errorf("unique should have 2 space-separated args, got %v", res.Pipes[0].Args)
	}
	// The filter pipe's condition is still extracted.
	found := false
	for _, c := range res.Conditions {
		if c.Field == "count" && c.PipeStage == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("filter condition not extracted: %+v", res.Conditions)
	}
}

// Comma-separated field lists must still work (and mix with the space form).
func TestRegressionCommaAndSpacePipeFields(t *testing.T) {
	for _, q := range []string{
		`process where true | unique a, b, c`,
		`process where true | unique a b c`,
		`process where true | sort x y`,
	} {
		res := ExtractConditions(q)
		if len(res.Errors) != 0 {
			t.Errorf("%q errored: %v", q, res.Errors)
		}
		if len(res.Pipes) != 1 || len(res.Pipes[0].Args) != len(res.Pipes[0].Args) {
			t.Errorf("%q pipes = %+v", q, res.Pipes)
		}
	}
	// unique with 3 space-separated fields yields 3 args.
	res := ExtractConditions(`process where true | unique a b c`)
	if len(res.Pipes[0].Args) != 3 {
		t.Errorf("args = %v, want 3", res.Pipes[0].Args)
	}
}
