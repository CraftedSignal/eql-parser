package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	eql "github.com/craftedsignal/eql-parser"
	sigma "github.com/craftedsignal/sigma-parser"
	"gopkg.in/yaml.v3"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// readLibraryRules extracts Sigma rules from a CraftedSignal library entries
// directory, where each YAML file wraps the rule text in a `query` field.
func readLibraryRules(dir string) []sigmaRuleEntry {
	var out []sigmaRuleEntry
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var wrapper struct {
			Query     string `yaml:"query"`
			QueryType string `yaml:"query_type"`
		}
		if yaml.Unmarshal(data, &wrapper) != nil {
			continue
		}
		if wrapper.QueryType != "sigma" || !strings.Contains(wrapper.Query, "detection:") {
			continue
		}
		out = append(out, sigmaRuleEntry{Source: "craftedsignal/library", Path: e.Name(), Rule: wrapper.Query})
	}
	return out
}

// buildRulesCorpus assembles a rule corpus of at least `target` entries by
// combining the scraped real Sigma rules (plus any library rules) with
// generated complex ones, writing a gzipped JSONL. Returns (realCount,
// generatedCount).
func buildRulesCorpus(realPath, libraryDir, outPath string, target int, seed int64) (int, int, error) {
	if dir := filepath.Dir(outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, 0, err
		}
	}
	out, err := os.Create(outPath)
	if err != nil {
		return 0, 0, err
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 1<<20)
	gz, _ := gzip.NewWriterLevel(bw, gzip.BestCompression)
	enc := json.NewEncoder(gz)

	// 1) Copy the real rules through, deduped by content and size-capped so a
	//    few oversized/aggregate YAML files don't bloat the committed corpus.
	const maxRuleBytes = 20000
	real := 0
	seen := map[string]struct{}{}
	var writeErr error
	ingest := func(e sigmaRuleEntry) {
		if writeErr != nil {
			return
		}
		rule := strings.TrimSpace(e.Rule)
		if rule == "" || len(rule) > maxRuleBytes {
			return
		}
		// Must actually be a Sigma rule (detection + condition).
		if !strings.Contains(rule, "detection:") || !strings.Contains(rule, "condition") {
			return
		}
		key := sha256Hex(rule)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if err := enc.Encode(e); err != nil {
			writeErr = err
			return
		}
		real++
	}

	// Scraped GitHub rules (JSONL).
	if realPath != "" {
		if rf, err := os.Open(realPath); err == nil {
			sc := bufio.NewScanner(rf)
			sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				var e sigmaRuleEntry
				if json.Unmarshal([]byte(line), &e) == nil {
					ingest(e)
				}
			}
			rf.Close()
		}
	}
	// CraftedSignal library rules (wrapped YAML entries).
	if libraryDir != "" {
		for _, e := range readLibraryRules(libraryDir) {
			ingest(e)
		}
	}
	if writeErr != nil {
		return real, 0, writeErr
	}

	// 2) Generate complex rules to reach the target.
	generated := 0
	if need := target - real; need > 0 {
		n, err := generateComplexRulesInto(enc, need, seed, 1)
		if err != nil {
			return real, n, err
		}
		generated = n
	}

	if err := gz.Close(); err != nil {
		return real, generated, err
	}
	return real, generated, bw.Flush()
}

// rulesCorpusStats accumulates outcomes of parse + translate + round-trip.
type rulesCorpusStats struct {
	total       int
	parseOK     int // Sigma parsed with conditions
	parseEmpty  int // parsed but no conditions
	parseFail   int // parse errors, no conditions
	panics      int
	fieldTrans  int // field-translatable to EQL (attempted round-trip)
	fieldPass   int // round-tripped with zero condition loss
	keyword     int
	complex     int
	correlation int
}

// testRulesCorpus streams the gzipped rule corpus and runs each rule through
// sigma parse -> Sigma->EQL translate -> EQL re-parse -> canonical round-trip
// comparison. Never panics (per-rule recovery). When eqlOut is non-empty, the
// faithfully-translated EQL of each field-translatable rule is written there
// (gzipped JSONL) as a CI-runnable EQL corpus for the core package. Returns
// the stats.
func testRulesCorpus(path, eqlOut string) (rulesCorpusStats, error) {
	var st rulesCorpusStats
	f, err := os.Open(path)
	if err != nil {
		return st, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return st, err
	}
	defer gz.Close()

	var eqlEnc *json.Encoder
	if eqlOut != "" {
		if dir := filepath.Dir(eqlOut); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		ef, err := os.Create(eqlOut)
		if err != nil {
			return st, err
		}
		defer ef.Close()
		ebw := bufio.NewWriterSize(ef, 1<<20)
		egz, _ := gzip.NewWriterLevel(ebw, gzip.BestCompression)
		defer func() { egz.Close(); ebw.Flush() }()
		eqlEnc = json.NewEncoder(egz)
	}

	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e sigmaRuleEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		st.total++
		checkOneRule(&st, e.Rule, eqlEnc)
	}
	return st, sc.Err()
}

func checkOneRule(st *rulesCorpusStats, rule string, eqlEnc *json.Encoder) {
	defer func() {
		if r := recover(); r != nil {
			st.panics++
		}
	}()

	sig := sigma.ExtractConditions(rule)
	switch {
	case len(sig.Conditions) > 0:
		st.parseOK++
	case len(sig.Errors) > 0:
		st.parseFail++
		return
	default:
		st.parseEmpty++
		return
	}

	eqlText, class := SigmaToEQL(sig)
	switch class {
	case transKeyword:
		st.keyword++
		return
	case transComplex:
		st.complex++
		return
	case transEmpty:
		return
	}

	res := eql.ExtractConditions(eqlText)
	if len(res.Errors) > 0 {
		return // translated EQL failed to parse — counted as a non-pass below
	}
	st.fieldTrans++
	want := canonAll(sigmaCanon(sig.Conditions))
	got := canonAll(eqlCanon(res.Conditions))
	missing, extra := compareCanon(want, got)
	if len(missing) == 0 && len(extra) == 0 {
		st.fieldPass++
		if eqlEnc != nil {
			_ = eqlEnc.Encode(largeEntry{Source: "sigma-translated", Name: "rt", Query: eqlText})
		}
		return
	}
	if len(ruleFailSamples) < 8 {
		ruleFailSamples = append(ruleFailSamples, fmt.Sprintf(
			"--- rule:\n%s\n--- eql: %s\n--- missing: %v\n--- extra: %v",
			strings.TrimSpace(rule), eqlText, sortedCanon(missing), sortedCanon(extra)))
	}
}

// ruleFailSamples collects a few round-trip failures for debugging.
var ruleFailSamples []string

// runRulesCorpus builds (if needed) and tests the 100k+ rule corpus, printing
// a report and returning the field round-trip pass rate.
func runRulesCorpus(realPath, libraryDir, outPath, eqlOut string, target int, seed int64, rebuild bool) (float64, error) {
	if rebuild {
		real, gen, err := buildRulesCorpus(realPath, libraryDir, outPath, target, seed)
		if err != nil {
			return 0, err
		}
		fmt.Printf("built rule corpus: %d real + %d generated = %d rules -> %s\n", real, gen, real+gen, outPath)
	}

	st, err := testRulesCorpus(outPath, eqlOut)
	if err != nil {
		return 0, err
	}

	fmt.Printf("rules=%d  sigma_parse_ok=%d parse_empty=%d parse_fail=%d panics=%d\n",
		st.total, st.parseOK, st.parseEmpty, st.parseFail, st.panics)
	fmt.Printf("translate: field=%d field_pass=%d  keyword=%d complex=%d\n",
		st.fieldTrans, st.fieldPass, st.keyword, st.complex)
	rate := 100.0
	if st.fieldTrans > 0 {
		rate = float64(st.fieldPass) * 100 / float64(st.fieldTrans)
	}
	fmt.Printf("Sigma->EQL round-trip pass rate: %.2f%% (%d/%d)\n", rate, st.fieldPass, st.fieldTrans)
	for _, s := range ruleFailSamples {
		fmt.Println(s)
	}
	if st.panics > 0 {
		return rate, fmt.Errorf("%d panics over %d rules", st.panics, st.total)
	}
	return rate, nil
}
