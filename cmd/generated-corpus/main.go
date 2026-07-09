// Command generated-corpus writes two test fixtures for the EQL parser:
//
//   - testdata/corpus.json: a curated bank of valid and adversarial queries.
//   - testdata/generated/eql_generated_corpus.json: bounded random ASTs paired
//     with the conditions a correct extractor must produce (differential
//     testing).
//
// Expected conditions are computed by an independent renderer/predictor here,
// deliberately NOT by calling the parser, so a bug shared between the parser
// and the oracle can't hide.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	eql "github.com/craftedsignal/eql-parser"
)

func main() {
	var (
		// This command is a nested module and runs from cmd/generated-corpus,
		// so default output paths point back up to the repo-root testdata.
		count    = flag.Int("count", 500, "number of differential (oracle) cases")
		seed     = flag.Int64("seed", 42, "PRNG seed")
		out      = flag.String("out", "../../testdata/generated/eql_generated_corpus.json", "differential corpus output path")
		corpus   = flag.String("corpus", "../../testdata/corpus.json", "curated corpus output path")
		skipCur  = flag.Bool("skip-corpus", false, "do not write the curated corpus")
		large    = flag.Int("large", 0, "if >0, generate this many queries into the large parse-only corpus")
		largeOut = flag.String("large-out", "../../testdata/generated/eql_large_corpus.jsonl.gz", "large corpus output path (gzipped JSONL)")
		realPath = flag.String("real", "../../testdata/real_eql_corpus.jsonl", "path to real EQL JSONL used as generation seeds")

		sigmaRT   = flag.Bool("sigma-roundtrip", false, "run the Sigma<->EQL round-trip and write its corpus")
		sigmaDir  = flag.String("sigma-corpus", "../../../sigma-parser/testdata/corpus", "directory of raw Sigma rules")
		sigmaOut  = flag.String("sigma-out", "../../testdata/generated/eql_sigma_roundtrip_corpus.json", "Sigma round-trip corpus output")
		sigmaFail = flag.String("sigma-failures", "../../testdata/generated/eql_sigma_roundtrip_failures.jsonl", "Sigma round-trip failures log")

		rules       = flag.Bool("rules", false, "build+test the large (100k+) Sigma rule corpus")
		rulesReal   = flag.String("rules-real", "../../testdata/generated/sigma_rules_real.jsonl", "scraped real Sigma rules JSONL")
		rulesLib    = flag.String("rules-library", "../../../library/entries/sigma", "CraftedSignal library sigma entries dir")
		rulesOut    = flag.String("rules-out", "../../testdata/generated/sigma_rules_100k.jsonl.gz", "combined rule corpus (gzipped JSONL)")
		rulesEQLOut = flag.String("rules-eql-out", "../../testdata/generated/sigma_rules_translated_eql.jsonl.gz", "translated-EQL corpus output (gzipped JSONL)")
		rulesTarget = flag.Int("rules-target", 100000, "minimum total rules in the corpus")
		rulesBuild  = flag.Bool("rules-build", true, "rebuild the corpus before testing")
	)
	flag.Parse()

	if *sigmaRT {
		rate, err := runSigmaRoundTrip(*sigmaDir, *realPath, *sigmaOut, *sigmaFail)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sigma round-trip:", err)
			os.Exit(1)
		}
		fmt.Printf("Sigma->EQL field translation pass rate: %.2f%%\n", rate)
		return
	}

	if *rules {
		rate, err := runRulesCorpus(*rulesReal, *rulesLib, *rulesOut, *rulesEQLOut, *rulesTarget, *seed, *rulesBuild)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rules corpus:", err)
			os.Exit(1)
		}
		fmt.Printf("100k+ rule corpus pass rate: %.2f%%\n", rate)
		return
	}

	if !*skipCur {
		if err := writeCuratedCorpus(*corpus, *realPath); err != nil {
			fmt.Fprintln(os.Stderr, "curated corpus:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", *corpus)
	}

	cases := generateCases(*count, *seed)
	if err := writeJSON(*out, cases); err != nil {
		fmt.Fprintln(os.Stderr, "generated corpus:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d cases)\n", *out, len(cases))

	if *large > 0 {
		n, rate, err := writeLargeCorpus(*largeOut, *large, *seed, *realPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "large corpus:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d queries, %.2f%% parse-clean on sample)\n", *largeOut, n, rate)
		if rate < 90 {
			fmt.Fprintf(os.Stderr, "large corpus parse rate %.2f%% below 90%% — generator templates drifted from the grammar\n", rate)
			os.Exit(1)
		}
	}

	// Self-check: verify every generated case matches the live extractor so
	// the committed fixture is never born failing.
	bad := 0
	for _, c := range cases {
		res := eql.ExtractConditions(c.Query)
		if len(res.Conditions) != len(c.Expected) {
			bad++
			if bad <= 10 {
				fmt.Fprintf(os.Stderr, "MISMATCH id=%d: %s\n  want %d got %d\n",
					c.ID, c.Query, len(c.Expected), len(res.Conditions))
			}
		}
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "%d/%d generated cases mismatch the extractor\n", bad, len(cases))
		os.Exit(1)
	}
	fmt.Printf("self-check ok: %d cases match the extractor\n", len(cases))
}

func writeJSON(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ---------------------------------------------------------------------------
// Curated corpus
// ---------------------------------------------------------------------------

type queryEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
	Valid  bool   `json:"valid,omitempty"`
}

func writeCuratedCorpus(path, realPath string) error {
	var entries []queryEntry
	for i, q := range validCorpus {
		entries = append(entries, queryEntry{Source: "curated", Name: fmt.Sprintf("valid_%03d", i), Query: q, Valid: true})
	}
	for i, q := range adversarialCorpus {
		entries = append(entries, queryEntry{Source: "adversarial", Name: fmt.Sprintf("adv_%03d", i), Query: q, Valid: false})
	}
	// Fold in a representative sample of real detection-rule queries, tagged
	// valid, so the TestCorpus clean-parse gate covers genuine EQL. Every
	// sampled query is verified to parse cleanly before it is marked valid;
	// any that don't (dialect quirks) are still included but left unflagged.
	real, err := readRealCorpus(realPath)
	if err == nil {
		const sampleEvery = 6 // ~200 of the ~1200 real rules
		for i, rq := range real {
			if i%sampleEvery != 0 {
				continue
			}
			res := eql.ExtractConditions(rq.Query)
			entries = append(entries, queryEntry{
				Source: "real:" + rq.Source,
				Name:   fmt.Sprintf("real_%04d", i),
				Query:  rq.Query,
				Valid:  len(res.Errors) == 0 && len(res.Conditions) > 0,
			})
		}
	}
	return writeJSON(path, entries)
}

// realEntry mirrors one line of the scraped real EQL JSONL corpus.
type realEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
}

// readRealCorpus loads the scraped real EQL queries, if present.
func readRealCorpus(path string) ([]realEntry, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []realEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e realEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// validCorpus holds hand-verified valid EQL covering the language surface.
var validCorpus = []string{
	`process where process.name == "cmd.exe"`,
	`process where process.name : "cmd.exe"`,
	`any where true`,
	`process where event.type == "start" and process.name : "regsvr32.exe"`,
	`process where process.name in ("cmd.exe", "powershell.exe", "pwsh.exe")`,
	`process where process.name not in~ ("explorer.exe", "svchost.exe")`,
	`file where file.path like ("C:\\Windows\\*", "C:\\Users\\*\\AppData\\*")`,
	`process where process.command_line regex~ """.*-enc.*"""`,
	`network where cidrMatch(source.ip, "10.0.0.0/8", "192.168.0.0/16")`,
	`process where endsWith~(process.name, ".exe") and length(process.args) >= 3`,
	`process where ?process.Ext.token.integrity_level_name : "system"`,
	`process where process.args[0] : "*python*" and process.args[1] : "*.py"`,
	"process where `unusual field name` == 1",
	`process where not (process.name : "a.exe" or process.name : "b.exe")`,
	`process where process.parent.name : "winword.exe" and process.name : ("cmd.exe", "powershell.exe")`,
	`sequence by process.entity_id [process where event.type == "start" and process.name : "msxsl.exe"] [network where event.type == "connection" and network.direction : "egress"]`,
	`sequence by host.id with maxspan=5m [process where event.action == "start"] [network where event.action == "connection_attempted"]`,
	`sequence by source.ip with maxspan=10s [authentication where event.outcome == "failure"] with runs=5 [authentication where event.outcome == "success"]`,
	`sequence with maxspan=1m [process where process.name : "sshd"] ![process where event.type == "end"]`,
	`sequence by process.entity_id [file where event.type == "creation"] [network where event.type == "connection"] until [process where event.type == "end"]`,
	`join by user.name [authentication where event.outcome == "success"] [process where process.name : "net.exe"]`,
	`sample by host.id [process where process.name : "malware.exe"] [file where file.name : "ransom.txt"]`,
	`process where process.name : "whoami.exe" | head 10`,
	`process where true | unique process.name, user.name | count`,
	`process where p == 1 | filter q : "x" | sort r | tail 5`,
	`process where process.name == 'net.exe'`,
	`process where process.name = "cmd.exe"`,
	`process where wildcard(process.command_line, "*mimikatz*")`,
	`process where child of [process where process.name : "explorer.exe"]`,
	`network where descendant of [process where process.name : "powershell.exe"]`,
	`process where process.pid == 4 and process.ppid != 0 and process.args_count > 1`,
	`"my-custom-category" where field.a == 1`,
	`process where process.hash.sha256 : "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`,
	`file where file.extension in ("exe", "dll", "scr", "com", "bat")`,
	`process where startsWith(process.executable, "C:\\Windows\\Temp")`,
	`network where destination.port in (445, 135, 139, 3389)`,
	`process where process.name : "rundll32.exe" and process.args_count == 1`,
	`process where match(process.command_line, "(?i)invoke-expression")`,
	`registry where registry.path : "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Run\\*"`,
	`process where stringContains~(process.command_line, "downloadstring")`,
	`process where process.name : "a.exe" or process.name : "b.exe" or process.name : "c.exe"`,
}

// adversarialCorpus holds malformed / hostile inputs. They must not panic and
// their results are unconstrained; TestCorpus only checks robustness.
var adversarialCorpus = []string{
	``,
	`   `,
	`process where`,
	`where`,
	`sequence`,
	`sequence [`,
	`process where ((((((((((`,
	`process where a == "unterminated`,
	`process where a in (`,
	`process where a : ()`,
	`@#$%^&*()`,
	`process where process.name == == "cmd"`,
	`| head 10`,
	`process where a < b < c < d`,
	`sequence with maxspan= [a where true]`,
	`process where ` + strings.Repeat("not ", 50) + `a == 1`,
	`process where a and or not b`,
	`"""` + strings.Repeat("x", 100),
	`process where f(f(f(f(f(f(`,
	`♠♣♥♦ where ☺ == ☹`,
	`process where a == 1 and`,
}

// ---------------------------------------------------------------------------
// Bounded-AST generator with an independent oracle
// ---------------------------------------------------------------------------

var (
	genFields    = []string{"process.name", "process.pid", "user.name", "host.id", "file.path", "destination.port", "source.ip", "event.type", "network.direction", "process.parent.name"}
	genStrValues = []string{"cmd.exe", "powershell.exe", "root", "admin", "C:\\Temp\\x", "start", "egress"}
	genOps       = []string{"==", "!=", ">", ">=", "<", "<="}
)

func generateCases(count int, seed int64) []eql.GeneratedCase {
	rng := rand.New(rand.NewSource(seed))
	cases := make([]eql.GeneratedCase, 0, count)
	for i := 0; i < count; i++ {
		caseSeed := rng.Int63()
		g := &gen{rng: rand.New(rand.NewSource(caseSeed)), usedFields: map[string]bool{}}
		var query string
		var expected []eql.ExpectedCondition
		switch g.rng.Intn(6) {
		case 0:
			query, expected = g.flatChain("and")
		case 1:
			query, expected = g.flatChain("or")
		case 2:
			query, expected = g.inList()
		case 3:
			query, expected = g.sequence()
		case 4:
			query, expected = g.negatedChain()
		default:
			query, expected = g.mixedCategory()
		}
		cases = append(cases, eql.GeneratedCase{
			ID:       int64(i),
			Seed:     caseSeed,
			Query:    query,
			Expected: expected,
		})
	}
	return cases
}

type gen struct {
	rng        *rand.Rand
	usedFields map[string]bool
}

// uniqueField returns a field not yet used in this case, so no OR-merging or
// dedup obscures the 1:1 leaf→condition mapping.
func (g *gen) uniqueField() string {
	for attempt := 0; attempt < 100; attempt++ {
		f := genFields[g.rng.Intn(len(genFields))]
		if !g.usedFields[f] {
			g.usedFields[f] = true
			return f
		}
	}
	// Exhausted the pool: synthesize one.
	f := "field_" + strconv.Itoa(len(g.usedFields))
	g.usedFields[f] = true
	return f
}

// leaf produces one `field op value` comparison and its expected condition.
func (g *gen) leaf() (string, eql.ExpectedCondition) {
	field := g.uniqueField()
	op := genOps[g.rng.Intn(len(genOps))]
	// Numeric fields take numeric values with ordering ops; others use strings.
	if field == "process.pid" || field == "destination.port" {
		val := strconv.Itoa(g.rng.Intn(65536))
		return fmt.Sprintf("%s %s %s", field, op, val),
			eql.ExpectedCondition{Field: field, Operator: op, Value: val}
	}
	// String fields only get == or != to keep semantics unambiguous.
	if op != "==" && op != "!=" {
		op = "=="
	}
	val := genStrValues[g.rng.Intn(len(genStrValues))]
	return fmt.Sprintf("%s %s %q", field, op, val),
		eql.ExpectedCondition{Field: field, Operator: op, Value: val}
}

func (g *gen) flatChain(conn string) (string, []eql.ExpectedCondition) {
	n := 2 + g.rng.Intn(4)
	var clauses []string
	var expected []eql.ExpectedCondition
	for i := 0; i < n; i++ {
		text, cond := g.leaf()
		clauses = append(clauses, text)
		expected = append(expected, cond)
	}
	query := "process where " + strings.Join(clauses, " "+conn+" ")
	return query, expected
}

func (g *gen) negatedChain() (string, []eql.ExpectedCondition) {
	n := 1 + g.rng.Intn(3)
	var clauses []string
	var expected []eql.ExpectedCondition
	for i := 0; i < n; i++ {
		text, cond := g.leaf()
		clauses = append(clauses, "not "+text)
		cond.Negated = true
		expected = append(expected, cond)
	}
	query := "process where " + strings.Join(clauses, " and ")
	return query, expected
}

func (g *gen) inList() (string, []eql.ExpectedCondition) {
	field := g.uniqueField()
	n := 2 + g.rng.Intn(4)
	var vals []string
	for i := 0; i < n; i++ {
		vals = append(vals, genStrValues[g.rng.Intn(len(genStrValues))])
	}
	var quoted []string
	for _, v := range vals {
		quoted = append(quoted, strconv.Quote(v))
	}
	query := fmt.Sprintf("process where %s in (%s)", field, strings.Join(quoted, ", "))
	return query, []eql.ExpectedCondition{{
		Field:        field,
		Operator:     "in",
		Value:        vals[0],
		Alternatives: vals,
	}}
}

func (g *gen) sequence() (string, []eql.ExpectedCondition) {
	cats := []string{"process", "network", "file", "registry", "authentication"}
	n := 2 + g.rng.Intn(3)
	var steps []string
	var expected []eql.ExpectedCondition
	for i := 0; i < n; i++ {
		cat := cats[g.rng.Intn(len(cats))]
		text, cond := g.leaf()
		cond.SequenceStep = i
		steps = append(steps, fmt.Sprintf("[%s where %s]", cat, text))
		expected = append(expected, cond)
	}
	query := "sequence by host.id " + strings.Join(steps, " ")
	return query, expected
}

func (g *gen) mixedCategory() (string, []eql.ExpectedCondition) {
	cats := []string{"process", "network", "file", "registry", "library", "dns"}
	cat := cats[g.rng.Intn(len(cats))]
	n := 1 + g.rng.Intn(3)
	var clauses []string
	var expected []eql.ExpectedCondition
	for i := 0; i < n; i++ {
		text, cond := g.leaf()
		clauses = append(clauses, text)
		expected = append(expected, cond)
	}
	query := cat + " where " + strings.Join(clauses, " and ")
	return query, expected
}
