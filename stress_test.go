package eql

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestParserStress generates a large volume of random structured queries and
// verifies the parser never panics and always returns well-formed results.
func TestParserStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed: %d", seed)

	const iterations = 100_000
	var (
		panics       int
		parseErrors  int
		withConds    int
		panicSamples []string
	)

	for i := 0; i < iterations; i++ {
		query := generateStressQuery(rng)
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					if len(panicSamples) < 10 {
						panicSamples = append(panicSamples, fmt.Sprintf("%q: %v", truncate(query, 100), r))
					}
				}
			}()
			res := ExtractConditions(query)
			if len(res.Errors) > 0 {
				parseErrors++
			}
			if len(res.Conditions) > 0 {
				withConds++
			}
			for _, c := range res.Conditions {
				if c.Field == "" || c.Operator == "" {
					t.Fatalf("malformed condition from %q: %+v", query, c)
				}
			}
		}()
	}

	t.Logf("iterations=%d panics=%d parse_errors=%d (%.1f%%) with_conditions=%d (%.1f%%)",
		iterations, panics, parseErrors, float64(parseErrors)*100/iterations,
		withConds, float64(withConds)*100/iterations)

	if panics > 0 {
		t.Errorf("parser panicked %d times", panics)
		for _, s := range panicSamples {
			t.Logf("  panic: %s", s)
		}
	}
}

// TestMutationStress mutates known-good queries in hostile ways (truncation,
// byte flips, quote/bracket surgery) and verifies robustness.
func TestMutationStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mutation stress in short mode")
	}
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed: %d", seed)

	seeds := append([]string{}, fuzzSeeds...)
	seeds = append(seeds, roundTripQueries...)
	seeds = append(seeds, realWorldQueries...)

	const perSeed = 2000
	panics := 0
	var panicSamples []string

	for _, base := range seeds {
		for i := 0; i < perSeed; i++ {
			mutated := mutate(rng, base)
			func() {
				defer func() {
					if r := recover(); r != nil {
						panics++
						if len(panicSamples) < 10 {
							panicSamples = append(panicSamples, fmt.Sprintf("%q: %v", truncate(mutated, 100), r))
						}
					}
				}()
				res := ExtractConditions(mutated)
				for _, c := range res.Conditions {
					if c.Field == "" || c.Operator == "" {
						t.Fatalf("malformed condition from mutation %q: %+v", mutated, c)
					}
				}
				// Also stress the standalone AST parser and renderer.
				if q, err := Parse(mutated); err == nil && q != nil {
					_ = q.String()
				}
			}()
		}
	}

	t.Logf("mutations=%d panics=%d", len(seeds)*perSeed, panics)
	if panics > 0 {
		t.Errorf("mutation stress panicked %d times", panics)
		for _, s := range panicSamples {
			t.Logf("  panic: %s", s)
		}
	}
}

// mutate applies a random hostile transformation to s.
func mutate(rng *rand.Rand, s string) string {
	if s == "" {
		return s
	}
	switch rng.Intn(8) {
	case 0: // truncate
		return s[:rng.Intn(len(s))]
	case 1: // flip a byte
		b := []byte(s)
		b[rng.Intn(len(b))] = byte(rng.Intn(256))
		return string(b)
	case 2: // duplicate a random span
		i := rng.Intn(len(s))
		j := i + rng.Intn(len(s)-i)
		return s[:j] + s[i:j] + s[j:]
	case 3: // delete a random char
		i := rng.Intn(len(s))
		return s[:i] + s[i+1:]
	case 4: // inject an unbalanced delimiter
		injects := []string{"(", ")", "[", "]", `"`, "`", "|", "![", "'"}
		i := rng.Intn(len(s) + 1)
		return s[:i] + injects[rng.Intn(len(injects))] + s[i:]
	case 5: // repeat a keyword-ish token
		toks := []string{" and ", " or ", " not ", " where ", " sequence ", " by ", " in "}
		i := rng.Intn(len(s) + 1)
		return s[:i] + strings.Repeat(toks[rng.Intn(len(toks))], 1+rng.Intn(3)) + s[i:]
	case 6: // strip all quotes
		return strings.NewReplacer(`"`, "", "'", "", "`", "").Replace(s)
	default: // splice two mangled halves
		i := rng.Intn(len(s))
		return s[i:] + s[:i]
	}
}

// generateStressQuery builds a random but syntactically-plausible EQL query.
func generateStressQuery(rng *rand.Rand) string {
	switch rng.Intn(10) {
	case 0, 1, 2, 3:
		return "process where " + genExpr(rng, 0)
	case 4, 5:
		cat := pick(rng, stressCategories)
		return cat + " where " + genExpr(rng, 0)
	case 6:
		return "any where " + genExpr(rng, 0)
	case 7:
		return genStressSequence(rng)
	case 8:
		return genStressSequence(rng) + genStressPipes(rng)
	default:
		return "process where " + genExpr(rng, 0) + genStressPipes(rng)
	}
}

func genStressSequence(rng *rand.Rand) string {
	kind := pick(rng, []string{"sequence", "join", "sample"})
	var b strings.Builder
	b.WriteString(kind)
	if rng.Intn(2) == 0 {
		b.WriteString(" by " + pick(rng, stressFields))
	}
	if kind == "sequence" && rng.Intn(2) == 0 {
		b.WriteString(fmt.Sprintf(" with maxspan=%d%s", 1+rng.Intn(60), pick(rng, []string{"s", "m", "h", "d"})))
	}
	n := 2 + rng.Intn(3)
	for i := 0; i < n; i++ {
		missing := kind == "sequence" && rng.Intn(5) == 0
		if missing {
			b.WriteString(" ![")
		} else {
			b.WriteString(" [")
		}
		b.WriteString(pick(rng, stressCategories) + " where " + genExpr(rng, 2))
		b.WriteString("]")
		if rng.Intn(3) == 0 {
			b.WriteString(" by " + pick(rng, stressFields))
		}
		if kind == "sequence" && !missing && rng.Intn(6) == 0 {
			b.WriteString(fmt.Sprintf(" with runs=%d", 1+rng.Intn(100)))
		}
	}
	if kind != "sample" && rng.Intn(4) == 0 {
		b.WriteString(" until [" + pick(rng, stressCategories) + " where " + genExpr(rng, 2) + "]")
	}
	return b.String()
}

func genStressPipes(rng *rand.Rand) string {
	var b strings.Builder
	n := 1 + rng.Intn(3)
	for i := 0; i < n; i++ {
		switch rng.Intn(6) {
		case 0:
			b.WriteString(fmt.Sprintf(" | head %d", rng.Intn(1000)))
		case 1:
			b.WriteString(fmt.Sprintf(" | tail %d", rng.Intn(1000)))
		case 2:
			b.WriteString(" | unique " + pick(rng, stressFields))
		case 3:
			b.WriteString(" | sort " + pick(rng, stressFields))
		case 4:
			b.WriteString(" | count")
		default:
			b.WriteString(" | filter " + genExpr(rng, 2))
		}
	}
	return b.String()
}

var (
	stressFields     = []string{"process.name", "process.pid", "user.name", "host.id", "a.b.c", "file.path", "destination.port"}
	stressCategories = []string{"process", "network", "file", "registry", "library", "dns", "authentication", "any"}
	stressStrings    = []string{"cmd.exe", "x", "C:\\a\\b", "*.dll", "10.0.0.0/8", ""}
)

func genExpr(rng *rand.Rand, depth int) string {
	if depth >= 4 || rng.Intn(depth+2) == 0 {
		return genLeaf(rng)
	}
	switch rng.Intn(6) {
	case 0:
		return genExpr(rng, depth+1) + " and " + genExpr(rng, depth+1)
	case 1:
		return genExpr(rng, depth+1) + " or " + genExpr(rng, depth+1)
	case 2:
		return "not " + genExpr(rng, depth+1)
	case 3:
		return "(" + genExpr(rng, depth+1) + ")"
	default:
		return genLeaf(rng)
	}
}

func genLeaf(rng *rand.Rand) string {
	field := pick(rng, stressFields)
	if rng.Intn(6) == 0 {
		field = "?" + field
	}
	switch rng.Intn(8) {
	case 0:
		return fmt.Sprintf("%s == %q", field, pick(rng, stressStrings))
	case 1:
		return fmt.Sprintf("%s : %q", field, pick(rng, stressStrings))
	case 2:
		return fmt.Sprintf("%s != %d", field, rng.Intn(65536))
	case 3:
		return fmt.Sprintf("%s in (%q, %q)", field, pick(rng, stressStrings), pick(rng, stressStrings))
	case 4:
		return fmt.Sprintf("%s like~ %q", field, pick(rng, stressStrings))
	case 5:
		return fmt.Sprintf("length(%s) > %d", field, rng.Intn(100))
	case 6:
		return fmt.Sprintf("cidrMatch(%s, %q)", field, "10.0.0.0/8")
	default:
		return field // bare boolean field
	}
}

func pick(rng *rand.Rand, opts []string) string {
	return opts[rng.Intn(len(opts))]
}
