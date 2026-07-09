package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	eql "github.com/craftedsignal/eql-parser"
)

// writeLargeCorpus streams n queries to a JSONL file. The corpus blends real
// detection-rule queries (verbatim and lightly mutated) with grammar-faithful
// synthetic queries spanning the full EQL surface, so a parser run over it
// exercises breadth at scale. Returns the count written and the clean-parse
// rate measured on a sample.
func writeLargeCorpus(path string, n int, seed int64, realPath string) (int, float64, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, 0, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var w interface {
		io.Writer
		Flush() error
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	// A .gz path is written gzip-compressed; the highly repetitive corpus
	// shrinks ~10x, keeping the committed (LFS) artifact a sane size.
	if strings.HasSuffix(path, ".gz") {
		gz, _ := gzip.NewWriterLevel(bw, gzip.BestCompression)
		w = gzipFlusher{gz, bw}
	} else {
		w = bw
	}
	defer w.Flush()

	real, _ := readRealCorpus(realPath)
	rng := rand.New(rand.NewSource(seed))
	enc := json.NewEncoder(w)

	// Parse-rate sampling: check roughly one in 250 generated (not real)
	// queries to confirm the templates stay grammar-faithful.
	sampled, clean := 0, 0

	for i := 0; i < n; i++ {
		var source, query string
		switch r := rng.Intn(100); {
		case len(real) > 0 && r < 15:
			// Verbatim real query.
			source = "real"
			query = real[rng.Intn(len(real))].Query
		case len(real) > 0 && r < 25:
			// Lightly, validly mutated real query.
			source = "real-mutated"
			query = mutateRealValid(rng, real[rng.Intn(len(real))].Query)
		default:
			source = "synthetic"
			query = genLargeQuery(rng, 0)
			if source == "synthetic" && sampled < 4000 && rng.Intn(250) == 0 {
				sampled++
				res := eql.ExtractConditions(query)
				if len(res.Errors) == 0 {
					clean++
				}
			}
		}
		if err := enc.Encode(largeEntry{
			Source: source,
			Name:   "gen_" + strconv.Itoa(i),
			Query:  query,
		}); err != nil {
			return i, 0, err
		}
	}

	rate := 100.0
	if sampled > 0 {
		rate = float64(clean) * 100 / float64(sampled)
	}
	return n, rate, nil
}

type largeEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
}

// gzipFlusher flushes the gzip writer and the underlying buffer in order.
type gzipFlusher struct {
	gz *gzip.Writer
	bw *bufio.Writer
}

func (g gzipFlusher) Write(p []byte) (int, error) { return g.gz.Write(p) }
func (g gzipFlusher) Flush() error {
	if err := g.gz.Close(); err != nil {
		return err
	}
	return g.bw.Flush()
}

// mutateRealValid applies a validity-preserving transformation to a real
// query, widening coverage without producing garbage.
func mutateRealValid(rng *rand.Rand, q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return q
	}
	switch rng.Intn(4) {
	case 0:
		return q + " | head " + strconv.Itoa(1+rng.Intn(1000))
	case 1:
		return q + " | tail " + strconv.Itoa(1+rng.Intn(500))
	case 2:
		// Append a benign additional constraint only to single-event queries
		// (appending inside a sequence would be invalid).
		if strings.HasPrefix(strings.ToLower(q), "sequence") ||
			strings.HasPrefix(strings.ToLower(q), "join") ||
			strings.HasPrefix(strings.ToLower(q), "sample") ||
			strings.Contains(q, "|") {
			return q
		}
		return q + " and " + genLeafLarge(rng)
	default:
		return q
	}
}

// ---------------------------------------------------------------------------
// Synthetic query generator — grammar-faithful across the full EQL surface.
// ---------------------------------------------------------------------------

var (
	lgCategories = []string{"process", "network", "file", "registry", "library", "dns", "authentication", "any", "api", "driver"}
	lgFields     = []string{
		"process.name", "process.executable", "process.command_line", "process.pid",
		"process.parent.name", "process.entity_id", "process.args", "process.args_count",
		"user.name", "user.id", "user.domain", "host.id", "host.name", "host.os.type",
		"file.path", "file.name", "file.extension", "file.hash.sha256",
		"destination.ip", "destination.port", "source.ip", "source.port", "source.bytes",
		"network.direction", "network.protocol", "dns.question.name",
		"registry.path", "registry.value", "event.type", "event.action", "event.code",
		"event.category", "event.outcome", "process.code_signature.trusted",
	}
	lgNumericFields = []string{"process.pid", "process.args_count", "destination.port", "source.port", "source.bytes", "event.code"}
	lgStrings       = []string{
		"cmd.exe", "powershell.exe", "explorer.exe", "svchost.exe", "lsass.exe",
		"C:\\Windows\\System32\\cmd.exe", "/usr/bin/bash", "*.dll", "*.exe",
		"4688", "4624", "start", "connection", "egress", "outbound",
		"10.0.0.0/8", "192.168.1.1", "cmd.exe -c whoami", "",
	}
	lgFunctions = []string{"length", "wildcard", "cidrMatch", "startsWith", "endsWith", "stringContains", "concat", "number", "match"}
)

func lgPick(rng *rand.Rand, opts []string) string { return opts[rng.Intn(len(opts))] }

func lgField(rng *rand.Rand) string {
	f := lgPick(rng, lgFields)
	switch rng.Intn(12) {
	case 0:
		return "?" + f // optional
	case 1:
		return f + "[" + strconv.Itoa(rng.Intn(4)) + "]" // array index
	case 2:
		return "`" + f + "`" // backtick (still a valid field name)
	default:
		return f
	}
}

func lgString(rng *rand.Rand) string {
	s := lgPick(rng, lgStrings)
	switch rng.Intn(10) {
	case 0:
		return `"""` + s + `"""` // raw
	case 1:
		return "'" + s + "'" // legacy single-quote
	default:
		return strconv.Quote(s)
	}
}

// genLargeQuery produces one random grammar-faithful EQL query.
func genLargeQuery(rng *rand.Rand, depth int) string {
	switch rng.Intn(12) {
	case 0, 1, 2, 3, 4:
		return genEventQueryLarge(rng) + genPipesLarge(rng)
	case 5, 6, 7:
		return genSequenceLarge(rng) + genPipesLarge(rng)
	case 8:
		return genJoinLarge(rng)
	case 9:
		return genSampleLarge(rng)
	case 10:
		return genEventQueryLarge(rng) // bare, no pipe
	default:
		return genBareExprLarge(rng)
	}
}

func genEventQueryLarge(rng *rand.Rand) string {
	cat := lgPick(rng, lgCategories)
	return cat + " where " + genExprLarge(rng, 0)
}

func genBareExprLarge(rng *rand.Rand) string {
	// A leading category-less expression (tolerated extension).
	return genExprLarge(rng, 0)
}

func genExprLarge(rng *rand.Rand, depth int) string {
	if depth >= 3 || rng.Intn(depth+2) == 0 {
		return genLeafLarge(rng)
	}
	switch rng.Intn(7) {
	case 0:
		return genExprLarge(rng, depth+1) + " and " + genExprLarge(rng, depth+1)
	case 1:
		return genExprLarge(rng, depth+1) + " or " + genExprLarge(rng, depth+1)
	case 2:
		return "not " + genLeafLarge(rng)
	case 3:
		return "(" + genExprLarge(rng, depth+1) + ")"
	default:
		return genLeafLarge(rng)
	}
}

func genLeafLarge(rng *rand.Rand) string {
	switch rng.Intn(16) {
	case 0:
		return lgField(rng) + " == " + lgString(rng)
	case 1:
		return lgField(rng) + " : " + lgString(rng)
	case 2:
		return lgField(rng) + " != " + lgString(rng)
	case 3:
		nf := lgPick(rng, lgNumericFields)
		return nf + " " + lgPick(rng, []string{">", ">=", "<", "<=", "=="}) + " " + strconv.Itoa(rng.Intn(65536))
	case 4:
		return lgField(rng) + " in (" + lgString(rng) + ", " + lgString(rng) + ")"
	case 5:
		return lgField(rng) + " in~ (" + lgString(rng) + ", " + lgString(rng) + ")"
	case 6:
		return lgField(rng) + " not in (" + lgString(rng) + ")"
	case 7:
		return lgField(rng) + " like " + lgString(rng)
	case 8:
		return lgField(rng) + " like~ (" + lgString(rng) + ", " + lgString(rng) + ")"
	case 9:
		return lgField(rng) + " regex " + lgString(rng)
	case 10:
		return lgField(rng) + " regex~ " + lgString(rng)
	case 11:
		fn := lgPick(rng, lgFunctions)
		return fn + "(" + lgField(rng) + ", " + lgString(rng) + ")"
	case 12:
		return "length(" + lgField(rng) + ") " + lgPick(rng, []string{">", ">=", "=="}) + " " + strconv.Itoa(rng.Intn(50))
	case 13:
		// field-to-field
		return lgField(rng) + " == " + lgField(rng)
	case 14:
		// arithmetic
		return lgPick(rng, lgNumericFields) + " + " + lgPick(rng, lgNumericFields) + " > " + strconv.Itoa(rng.Intn(1000))
	default:
		// existence / boolean field / null check
		switch rng.Intn(3) {
		case 0:
			return "?" + lgPick(rng, lgFields) + " != null"
		case 1:
			return "?" + lgPick(rng, lgFields) + " == null"
		default:
			return lgField(rng)
		}
	}
}

func genPipesLarge(rng *rand.Rand) string {
	if rng.Intn(3) != 0 {
		return ""
	}
	var b strings.Builder
	n := 1 + rng.Intn(3)
	for i := 0; i < n; i++ {
		switch rng.Intn(7) {
		case 0:
			b.WriteString(" | head " + strconv.Itoa(1+rng.Intn(1000)))
		case 1:
			b.WriteString(" | tail " + strconv.Itoa(1+rng.Intn(500)))
		case 2:
			b.WriteString(" | unique " + lgPick(rng, lgFields))
		case 3:
			b.WriteString(" | unique_count " + lgPick(rng, lgFields))
		case 4:
			b.WriteString(" | sort " + lgPick(rng, lgFields))
		case 5:
			b.WriteString(" | count")
		default:
			b.WriteString(" | filter " + genLeafLarge(rng))
		}
	}
	return b.String()
}

func genSequenceLarge(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("sequence")

	hasMaxspan := rng.Intn(2) == 0
	byFirst := rng.Intn(2) == 0
	if byFirst && rng.Intn(2) == 0 {
		b.WriteString(" by " + genByKeysLarge(rng))
		if hasMaxspan {
			b.WriteString(" with maxspan=" + genMaxspanLarge(rng))
		}
	} else if hasMaxspan {
		b.WriteString(" with maxspan=" + genMaxspanLarge(rng))
		if rng.Intn(2) == 0 {
			b.WriteString(" by " + genByKeysLarge(rng))
		}
	}

	nSteps := 2 + rng.Intn(3)
	// Missing events require maxspan; only emit them when we have it.
	for i := 0; i < nSteps; i++ {
		missing := hasMaxspan && i > 0 && rng.Intn(6) == 0
		if missing {
			b.WriteString(" ![")
		} else {
			b.WriteString(" [")
		}
		b.WriteString(lgPick(rng, lgCategories) + " where " + genExprLarge(rng, 1))
		b.WriteString("]")
		if !missing && rng.Intn(4) == 0 {
			b.WriteString(" with runs=" + strconv.Itoa(1+rng.Intn(100)))
		}
	}
	if rng.Intn(4) == 0 {
		b.WriteString(" until [" + lgPick(rng, lgCategories) + " where " + genExprLarge(rng, 1) + "]")
	}
	return b.String()
}

func genJoinLarge(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("join")
	if rng.Intn(2) == 0 {
		b.WriteString(" by " + genByKeysLarge(rng))
	}
	n := 2 + rng.Intn(2)
	for i := 0; i < n; i++ {
		b.WriteString(" [" + lgPick(rng, lgCategories) + " where " + genExprLarge(rng, 1) + "]")
	}
	if rng.Intn(4) == 0 {
		b.WriteString(" until [" + lgPick(rng, lgCategories) + " where " + genExprLarge(rng, 1) + "]")
	}
	return b.String()
}

func genSampleLarge(rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("sample by " + genByKeysLarge(rng))
	n := 2 + rng.Intn(2)
	for i := 0; i < n; i++ {
		b.WriteString(" [" + lgPick(rng, lgCategories) + " where " + genExprLarge(rng, 1) + "]")
	}
	return b.String()
}

func genByKeysLarge(rng *rand.Rand) string {
	n := 1 + rng.Intn(2)
	var keys []string
	for i := 0; i < n; i++ {
		keys = append(keys, lgPick(rng, lgFields))
	}
	return strings.Join(keys, ", ")
}

func genMaxspanLarge(rng *rand.Rand) string {
	return strconv.Itoa(1+rng.Intn(60)) + lgPick(rng, []string{"s", "m", "h", "d", "ms"})
}
