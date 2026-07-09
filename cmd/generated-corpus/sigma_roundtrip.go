package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	eql "github.com/craftedsignal/eql-parser"
	sigma "github.com/craftedsignal/sigma-parser"
	"gopkg.in/yaml.v3"
)

// roundTripEntry records one faithful translation for the committed corpus.
type roundTripEntry struct {
	Direction string `json:"direction"` // "sigma->eql" or "eql->sigma"
	Source    string `json:"source"`
	Class     string `json:"class"`
	Input     string `json:"input"`
	Output    string `json:"output"`
	Condition int    `json:"conditions"`
}

// roundTripFailure records a mismatch for debugging.
type roundTripFailure struct {
	Direction string   `json:"direction"`
	Source    string   `json:"source"`
	Stage     string   `json:"stage"`
	Input     string   `json:"input"`
	Output    string   `json:"output,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	Extra     []string `json:"extra,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

// sigmaRoundTripStats accumulates outcomes by category.
type sigmaRoundTripStats struct {
	total       int
	sigmaParse  int // Sigma rule failed to parse
	field       int // pure-field rules attempted
	fieldPass   int // pure-field rules that round-tripped faithfully
	keyword     int // skipped: free-text keyword rules
	complex     int // skipped: escaped-wildcard rules with no faithful EQL form
	aggregation int // partial: aggregation rules (conditions compared)
	aggPass     int
	empty       int
}

// runSigmaRoundTrip drives Sigma->EQL over a raw Sigma corpus directory and
// EQL->Sigma over the real EQL corpus, writing the passing corpus + a failure
// log and returning the field-translation pass rate (the "perfect" metric).
func runSigmaRoundTrip(sigmaDir, realPath, outCorpus, outFailures string) (float64, error) {
	var entries []roundTripEntry
	var failures []roundTripFailure

	// ---- Direction A: Sigma -> EQL over the raw Sigma corpus ----
	var st sigmaRoundTripStats
	files, err := sigmaYAMLFiles(sigmaDir)
	if err != nil {
		return 0, err
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		st.total++
		name := filepath.Base(path)

		sig := sigma.ExtractConditions(string(content))
		if len(sig.Errors) > 0 && len(sig.Conditions) == 0 {
			st.sigmaParse++
			continue
		}
		if len(sig.Conditions) == 0 {
			st.empty++
			continue
		}

		eqlText, class := SigmaToEQL(sig)
		switch class {
		case transKeyword:
			st.keyword++
			continue
		case transComplex:
			st.complex++
			continue
		case transEmpty:
			st.empty++
			continue
		}

		res := eql.ExtractConditions(eqlText)
		if len(res.Errors) > 0 {
			failures = append(failures, roundTripFailure{
				Direction: "sigma->eql", Source: name, Stage: "eql_parse",
				Input: firstLine(string(content)), Output: eqlText, Errors: res.Errors,
			})
			recordAttempt(&st, class, false)
			continue
		}

		want := canonAll(sigmaCanon(sig.Conditions))
		got := canonAll(eqlCanon(res.Conditions))
		missing, extra := compareCanon(want, got)
		pass := len(missing) == 0 && len(extra) == 0
		recordAttempt(&st, class, pass)
		if pass {
			entries = append(entries, roundTripEntry{
				Direction: "sigma->eql", Source: name, Class: string(class),
				Input: compact(string(content)), Output: eqlText, Condition: len(res.Conditions),
			})
		} else {
			failures = append(failures, roundTripFailure{
				Direction: "sigma->eql", Source: name, Stage: "mismatch",
				Input: firstLine(string(content)), Output: eqlText,
				Missing: sortedCanon(missing), Extra: sortedCanon(extra),
			})
		}
	}

	// ---- Direction B: EQL -> Sigma over the real EQL corpus ----
	var bTotal, bField, bPass, bCorrelation, bKeyword int
	real, _ := readRealCorpus(realPath)
	for _, rq := range real {
		res := eql.ExtractConditions(rq.Query)
		if len(res.Errors) > 0 {
			continue
		}
		bTotal++
		det, class, ok := EQLToSigmaDetection(res)
		if !ok {
			switch class {
			case translateability("correlation"):
				bCorrelation++
			case transKeyword:
				bKeyword++
			}
			continue
		}
		bField++

		yml, err := marshalSigma(rq.Name, det)
		if err != nil {
			failures = append(failures, roundTripFailure{Direction: "eql->sigma", Source: rq.Name, Stage: "marshal", Input: rq.Query, Errors: []string{err.Error()}})
			continue
		}
		sig := sigma.ExtractConditions(yml)
		if len(sig.Errors) > 0 && len(sig.Conditions) == 0 {
			failures = append(failures, roundTripFailure{Direction: "eql->sigma", Source: rq.Name, Stage: "sigma_parse", Input: rq.Query, Output: yml, Errors: sig.Errors})
			continue
		}
		// Compare only the field conditions we actually translated: pipe-stage
		// (aggregation) conditions have no Sigma selection form.
		want := canonAll(eqlCanon(nonPipeConditions(res.Conditions)))
		got := canonAll(sigmaCanon(sig.Conditions))
		missing, extra := compareCanon(want, got)
		if len(missing) == 0 && len(extra) == 0 {
			bPass++
			entries = append(entries, roundTripEntry{
				Direction: "eql->sigma", Source: rq.Name, Class: string(class),
				Input: rq.Query, Output: compact(yml), Condition: len(sig.Conditions),
			})
		} else {
			failures = append(failures, roundTripFailure{
				Direction: "eql->sigma", Source: rq.Name, Stage: "mismatch",
				Input: rq.Query, Output: yml, Missing: sortedCanon(missing), Extra: sortedCanon(extra),
			})
		}
	}

	// ---- Report ----
	fieldRate := 100.0
	if st.field > 0 {
		fieldRate = float64(st.fieldPass) * 100 / float64(st.field)
	}
	bRate := 100.0
	if bField > 0 {
		bRate = float64(bPass) * 100 / float64(bField)
	}
	fmt.Printf("Sigma->EQL: corpus=%d sigma_parse_fail=%d empty=%d keyword_skipped=%d complex_skipped=%d\n",
		st.total, st.sigmaParse, st.empty, st.keyword, st.complex)
	fmt.Printf("Sigma->EQL: field=%d field_pass=%d (%.2f%%)  aggregation=%d agg_pass=%d\n",
		st.field, st.fieldPass, fieldRate, st.aggregation, st.aggPass)
	fmt.Printf("EQL->Sigma: real=%d translatable=%d pass=%d (%.2f%%)  correlation_skipped=%d keyword_skipped=%d\n",
		bTotal, bField, bPass, bRate, bCorrelation, bKeyword)

	if err := writeJSON(outCorpus, entries); err != nil {
		return 0, err
	}
	if err := writeJSONL(outFailures, failures); err != nil {
		return 0, err
	}
	fmt.Printf("wrote %s (%d faithful translations), %s (%d failures)\n",
		outCorpus, len(entries), outFailures, len(failures))

	// The reported metric is the pure-field Sigma->EQL pass rate — the honest
	// "perfect translation" number for rules that CAN cross the divide.
	return fieldRate, nil
}

func recordAttempt(st *sigmaRoundTripStats, class translateability, pass bool) {
	switch class {
	case transField:
		st.field++
		if pass {
			st.fieldPass++
		}
	case transAggregate:
		st.aggregation++
		st.field++ // aggregation rules still have field conditions to verify
		if pass {
			st.aggPass++
			st.fieldPass++
		}
	}
}

// marshalSigma renders a detection map as a complete Sigma rule document.
func marshalSigma(title string, det map[string]any) (string, error) {
	rule := eqlSigmaRule{
		Title:     title,
		Status:    "experimental",
		Logsource: map[string]string{"product": "generic"},
		Detection: det,
	}
	b, err := yaml.Marshal(rule)
	return string(b), err
}

func sigmaCanon(cs []sigma.Condition) [][]canonCond {
	out := make([][]canonCond, 0, len(cs))
	for _, c := range cs {
		out = append(out, canonFromSigma(c))
	}
	return out
}

// nonPipeConditions returns the conditions that filter the main event stream
// (pipe-stage aggregation conditions have no Sigma selection analog).
func nonPipeConditions(cs []eql.Condition) []eql.Condition {
	out := make([]eql.Condition, 0, len(cs))
	for _, c := range cs {
		if c.PipeStage == 0 {
			out = append(out, c)
		}
	}
	return out
}

func eqlCanon(cs []eql.Condition) [][]canonCond {
	out := make([][]canonCond, 0, len(cs))
	for _, c := range cs {
		out = append(out, canonFromEQL(c))
	}
	return out
}

func canonAll(groups [][]canonCond) []canonCond {
	var out []canonCond
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func sigmaYAMLFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func writeJSONL(path string, v []roundTripFailure) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	enc := json.NewEncoder(w)
	for _, e := range v {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
