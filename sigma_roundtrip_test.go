package eql

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const sigmaRoundTripPath = "testdata/generated/eql_sigma_roundtrip_corpus.json"

// sigmaRoundTripEntry mirrors one committed faithful translation. The corpus
// is produced by `make sigma-corpus`, which verifies each entry round-trips
// through both dialects (Sigma<->EQL) with no condition loss; only passing
// entries are written. This test re-validates the committed artifact from the
// core package (which cannot import sigma-parser) by confirming the EQL side of
// every entry parses cleanly and yields the recorded conditions.
type sigmaRoundTripEntry struct {
	Direction  string `json:"direction"`
	Source     string `json:"source"`
	Class      string `json:"class"`
	Input      string `json:"input"`
	Output     string `json:"output"`
	Conditions int    `json:"conditions"`
}

func TestSigmaRoundTripCorpus(t *testing.T) {
	skipIfLFSPointer(t, sigmaRoundTripPath)
	data, err := os.ReadFile(sigmaRoundTripPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("sigma round-trip corpus not available at %s — run 'make sigma-corpus'", sigmaRoundTripPath)
		}
		t.Fatalf("read corpus: %v", err)
	}
	var entries []sigmaRoundTripEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("sigma round-trip corpus is empty")
	}
	t.Logf("loaded %d faithful Sigma<->EQL translations", len(entries))

	var sigmaToEQL, eqlToSigma int
	for _, e := range entries {
		// The EQL text is the Output for sigma->eql and the Input for
		// eql->sigma; in both cases it must parse cleanly with conditions.
		eqlText := e.Output
		if e.Direction == "eql->sigma" {
			eqlText = e.Input
			eqlToSigma++
		} else {
			sigmaToEQL++
		}
		res := ExtractConditions(eqlText)
		if len(res.Errors) > 0 {
			t.Errorf("%s [%s]: committed EQL failed to parse: %v\n  %s",
				e.Source, e.Direction, res.Errors, truncate(eqlText, 160))
			continue
		}
		if e.Direction == "sigma->eql" && e.Conditions > 0 && len(res.Conditions) == 0 {
			t.Errorf("%s: committed EQL yielded no conditions (expected %d): %s",
				e.Source, e.Conditions, truncate(eqlText, 160))
		}
		for i, c := range res.Conditions {
			if c.Field == "" || c.Operator == "" {
				t.Errorf("%s: malformed condition %d: %+v", e.Source, i, c)
			}
		}
	}
	t.Logf("re-validated sigma->eql=%d, eql->sigma=%d", sigmaToEQL, eqlToSigma)

	// The generator writes the failures log alongside; it must be empty for a
	// committed corpus (every translatable rule round-tripped).
	if fdata, err := os.ReadFile("testdata/generated/eql_sigma_roundtrip_failures.jsonl"); err == nil {
		if n := strings.Count(strings.TrimSpace(string(fdata)), "\n"); n > 0 || len(strings.TrimSpace(string(fdata))) > 0 {
			t.Errorf("sigma round-trip failures log is non-empty (%d lines) — regenerate with 'make sigma-corpus'", n+1)
		}
	}
}
