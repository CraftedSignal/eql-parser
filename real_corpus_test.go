package eql

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

const realCorpusPath = "testdata/real_eql_corpus.jsonl"

type realCorpusEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
}

func loadRealCorpus(t testing.TB) []realCorpusEntry {
	t.Helper()
	f, err := os.Open(realCorpusPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("real corpus not available at %s", realCorpusPath)
		}
		t.Fatalf("open real corpus: %v", err)
	}
	defer f.Close()
	var out []realCorpusEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e realCorpusEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad corpus line: %v", err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

// TestRealCorpus parses every real EQL detection rule (elastic/detection-rules
// + endgameinc/eqllib) and asserts a high clean-parse rate with zero panics.
// This is the ground-truth measure that the parser handles genuine EQL, not
// just synthetic queries.
func TestRealCorpus(t *testing.T) {
	queries := loadRealCorpus(t)
	t.Logf("loaded %d real queries", len(queries))

	var clean, partial, failed, noConds, panics int
	errorKinds := map[string]int{}
	var failures []string

	for _, q := range queries {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Errorf("PANIC on %s: %v", q.Name, r)
				}
			}()
			res := ExtractConditions(q.Query)
			for i, c := range res.Conditions {
				if c.Field == "" || c.Operator == "" {
					t.Errorf("%s: malformed condition %d: %+v", q.Name, i, c)
				}
			}
			switch {
			case len(res.Errors) == 0 && len(res.Conditions) > 0:
				clean++
			case len(res.Errors) == 0:
				noConds++
			case len(res.Conditions) > 0:
				partial++
			default:
				failed++
				if len(failures) < 15 {
					failures = append(failures, q.Name+": "+strings.Join(res.Errors, "; "))
				}
			}
			for _, e := range res.Errors {
				errorKinds[errorKind(e)]++
			}
		}()
	}

	total := len(queries)
	// "clean" here means: parsed with no errors. A rule may legitimately
	// yield no conditions (e.g. `any where true`), so count clean+noConds as
	// successfully parsed.
	parsed := clean + noConds
	t.Logf("clean=%d no_conditions=%d partial=%d failed=%d panics=%d (total=%d)",
		clean, noConds, partial, failed, panics, total)
	t.Logf("clean-parse rate: %.2f%% (%d/%d)", float64(parsed)*100/float64(total), parsed, total)
	t.Logf("condition-extraction rate: %.2f%% (%d/%d)", float64(clean)*100/float64(total), clean, total)

	if len(errorKinds) > 0 {
		t.Logf("error kinds:")
		type kc struct {
			k string
			c int
		}
		var kcs []kc
		for k, c := range errorKinds {
			kcs = append(kcs, kc{k, c})
		}
		sort.Slice(kcs, func(i, j int) bool { return kcs[i].c > kcs[j].c })
		for i, x := range kcs {
			if i >= 12 {
				break
			}
			t.Logf("  %4d  %s", x.c, x.k)
		}
	}
	for _, f := range failures {
		t.Logf("FAILED %s", truncate(f, 160))
	}

	if panics > 0 {
		t.Errorf("parser panicked on %d real queries", panics)
	}
	// Real detection rules are valid EQL by construction; the parser handles
	// 100% of the current corpus, so the gate is set just below to tolerate a
	// single future oddity without masking a real regression.
	rate := float64(parsed) * 100 / float64(total)
	if rate < 99.5 {
		t.Errorf("real-corpus clean-parse rate %.2f%% below 99.5%% threshold", rate)
	}
}

// errorKind reduces an error message to a stable category for aggregation.
func errorKind(e string) string {
	// Drop the "line X:Y:" prefix and any quoted specifics.
	if i := strings.Index(e, ": "); i >= 0 && strings.HasPrefix(e, "line ") {
		e = e[i+2:]
	}
	// Collapse quoted tokens.
	var b strings.Builder
	inQuote := false
	for _, r := range e {
		switch r {
		case '"', '\'', '`':
			if !inQuote {
				b.WriteString("'…'")
			}
			inQuote = !inQuote
		default:
			if !inQuote {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}
