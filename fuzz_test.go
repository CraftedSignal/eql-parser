package eql

import (
	"testing"
	"time"
)

// fuzzSeeds are inputs exercising every corner of the grammar, used to seed
// both native fuzzing and the stress harness.
var fuzzSeeds = []string{
	`process where process.name == "cmd.exe"`,
	`any where true`,
	`process where process.name in ("a", "b", "c")`,
	`process where not process.name : "x" and length(args) > 2`,
	`sequence by host.id with maxspan=30s [a where b == 1] [c where d == 2] until [e where f == 3]`,
	`sequence with maxspan=1m [a where true] ![b where true]`,
	`join by k [a where true] [b where true]`,
	`sample by k [a where true] [b where true]`,
	`process where true | head 10 | tail 5 | unique x | count`,
	`process where a.b.c[0] : "*" and ?d.e != null`,
	`process where child of [process where x == 1]`,
	`file where file.path like ("C:\\*", "D:\\*")`,
	`process where a regex~ """.*\d+"""`,
	"process where `weird field` == 1",
	`process where a == -1 or b == +2.5e3`,
	`"cat-with-dashes" where x : "y"`,
	`process where wildcard(cmd, "*a*", "*b*")`,
	`process where a in~ ("X") or b not in (1, 2)`,
	`process where process.name = 'legacy'`,
	`process where a == 1 and (b == 2 or c == 3) and not d : "e"`,
	``,
	`@#$%`,
	`sequence [`,
	`process where ((((`,
}

// FuzzExtractConditions checks that extraction never panics, never hangs, and
// always returns a well-formed result on arbitrary input.
func FuzzExtractConditions(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if len(query) > 100_000 {
			return
		}
		start := time.Now()
		res := ExtractConditions(query)
		if elapsed := time.Since(start); elapsed > 8*time.Second {
			t.Errorf("extraction took %s for %q", elapsed, truncate(query, 80))
		}
		if res == nil {
			t.Fatal("nil result")
		}
		if res.Conditions == nil {
			t.Error("Conditions slice is nil")
		}
		for i, c := range res.Conditions {
			if c.Field == "" {
				t.Errorf("condition %d has empty field", i)
			}
			if c.Operator == "" {
				t.Errorf("condition %d has empty operator", i)
			}
			if c.LogicalOp != "" && c.LogicalOp != "AND" && c.LogicalOp != "OR" {
				t.Errorf("condition %d has invalid logical op %q", i, c.LogicalOp)
			}
		}
		// Every join key and category must also appear well-formed.
		for _, cat := range res.EventCategories {
			if cat == "" {
				t.Error("empty event category recorded")
			}
		}
	})
}

// FuzzParse checks that the AST parser never panics and that any AST it
// returns renders to something re-parseable (render must not produce text the
// parser rejects outright with new errors it didn't already have).
func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if len(query) > 100_000 {
			return
		}
		q, err := Parse(query)
		if q == nil {
			if err == nil {
				t.Fatal("nil query with nil error")
			}
			return
		}
		// Rendering must never panic and must be idempotent on a clean parse.
		rendered := q.String()
		if err == nil {
			q2, err2 := Parse(rendered)
			if err2 != nil {
				t.Errorf("clean parse rendered to unparseable text\n  in:  %q\n  out: %q\n  err: %v",
					truncate(query, 80), truncate(rendered, 80), err2)
				return
			}
			if q2.String() != rendered {
				t.Errorf("render not idempotent\n  r1: %q\n  r2: %q", rendered, q2.String())
			}
		}
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
