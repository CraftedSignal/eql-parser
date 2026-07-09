package eql

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The large corpus is committed gzip-compressed (LFS); a plain .jsonl is used
// as a fallback when present (e.g. mid-regeneration).
const largeCorpusPath = "testdata/generated/eql_large_corpus.jsonl.gz"
const largeCorpusPathPlain = "testdata/generated/eql_large_corpus.jsonl"

// largeCorpusEntry is one line of the streaming JSONL large corpus.
type largeCorpusEntry struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Query  string `json:"query"`
}

// streamLargeCorpus invokes fn for every entry in the JSONL corpus without
// loading the whole file into memory. It reads the gzipped corpus, or a plain
// .jsonl if that is what exists. Returns the number of entries seen.
func streamLargeCorpus(t testing.TB, fn func(largeCorpusEntry)) int {
	t.Helper()
	var r io.Reader
	if f, err := os.Open(largeCorpusPath); err == nil {
		defer f.Close()
		gz, gzErr := gzip.NewReader(f)
		if gzErr != nil {
			t.Fatalf("open gzip corpus: %v", gzErr)
		}
		defer gz.Close()
		r = gz
	} else if f, err := os.Open(largeCorpusPathPlain); err == nil {
		defer f.Close()
		r = f
	} else {
		t.Skipf("large corpus not available at %s — run 'make corpus'", largeCorpusPath)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // allow long query lines
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e largeCorpusEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("corpus line %d: %v", n+1, err)
		}
		fn(e)
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan corpus: %v", err)
	}
	return n
}

// TestLargeCorpus runs the parser over the entire large corpus with a pool of
// workers, verifying it never panics and reporting a parse-rate breakdown.
func TestLargeCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large corpus in short mode")
	}

	var (
		total       int64
		success     int64
		partial     int64
		failed      int64
		noConds     int64
		panics      int64
		conditions  int64
		malformed   int64
		longest     int64
		parseTimeNS int64
	)

	numWorkers := runtime.NumCPU()
	ch := make(chan largeCorpusEntry, 1024)
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range ch {
				func() {
					defer func() {
						if r := recover(); r != nil {
							atomic.AddInt64(&panics, 1)
						}
					}()
					start := time.Now()
					res := ExtractConditions(q.Query)
					atomic.AddInt64(&parseTimeNS, int64(time.Since(start)))
					atomic.AddInt64(&total, 1)
					atomic.AddInt64(&conditions, int64(len(res.Conditions)))
					for _, c := range res.Conditions {
						if c.Field == "" || c.Operator == "" {
							atomic.AddInt64(&malformed, 1)
						}
					}
					switch {
					case len(res.Conditions) > 0 && len(res.Errors) == 0:
						atomic.AddInt64(&success, 1)
					case len(res.Conditions) > 0:
						atomic.AddInt64(&partial, 1)
					case len(res.Errors) > 0:
						atomic.AddInt64(&failed, 1)
					default:
						atomic.AddInt64(&noConds, 1)
					}
					if l := int64(len(q.Query)); l > atomic.LoadInt64(&longest) {
						atomic.StoreInt64(&longest, l)
					}
				}()
			}
		}()
	}

	startTime := time.Now()
	n := streamLargeCorpus(t, func(e largeCorpusEntry) { ch <- e })
	close(ch)
	wg.Wait()
	wall := time.Since(startTime)

	t.Logf("=== Large Corpus Results ===")
	t.Logf("queries:           %d", total)
	t.Logf("full success:      %d (%.1f%%)", success, pct(success, total))
	t.Logf("partial:           %d (%.1f%%)", partial, pct(partial, total))
	t.Logf("no conditions:     %d (%.1f%%)", noConds, pct(noConds, total))
	t.Logf("failed:            %d (%.1f%%)", failed, pct(failed, total))
	t.Logf("panics:            %d", panics)
	t.Logf("conditions total:  %d (avg %.2f)", conditions, float64(conditions)/float64(max64(total, 1)))
	t.Logf("longest query:     %d bytes", longest)
	t.Logf("wall time:         %v (%.0f queries/sec)", wall, float64(total)/wall.Seconds())
	if total > 0 {
		t.Logf("avg parse time:    %v", time.Duration(parseTimeNS/total))
	}

	if n < 1 {
		t.Fatal("corpus was empty")
	}
	if panics > 0 {
		t.Errorf("parser panicked on %d queries — must be zero", panics)
	}
	if malformed > 0 {
		t.Errorf("%d conditions had empty field/operator — must be zero", malformed)
	}
	// The corpus is dominated by generator output that is valid by
	// construction; combined with real rules, the testable parse rate must
	// stay high.
	testable := success + partial + failed
	if testable > 0 {
		rate := float64(success+partial) * 100 / float64(testable)
		t.Logf("parse rate (of testable): %.2f%%", rate)
		if rate < 90.0 {
			t.Errorf("parse rate %.2f%% below 90%% threshold", rate)
		}
	}
}

// TestLargeCorpusRoundTripSample re-renders and re-parses a sample of the
// corpus, asserting the parse→render→parse fixpoint holds at scale.
func TestLargeCorpusRoundTripSample(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	const sampleEvery = 37
	var checked, unstable int64
	i := 0
	streamLargeCorpus(t, func(e largeCorpusEntry) {
		i++
		if i%sampleEvery != 0 {
			return
		}
		q1, err := Parse(NormalizeQuery(e.Query))
		if err != nil || q1 == nil {
			return
		}
		r1 := q1.String()
		q2, err := Parse(r1)
		if err != nil || q2 == nil {
			return
		}
		checked++
		if r1 != q2.String() {
			unstable++
			if unstable <= 10 {
				t.Errorf("round-trip unstable for %q:\n  r1: %s\n  r2: %s", e.Name, r1, q2.String())
			}
		}
	})
	t.Logf("round-trip sample: checked=%d unstable=%d", checked, unstable)
}

// BenchmarkLargeCorpus benchmarks extraction over a bounded prefix of the
// corpus.
func BenchmarkLargeCorpus(b *testing.B) {
	const limit = 2000
	var queries []string
	i := 0
	streamLargeCorpus(b, func(e largeCorpusEntry) {
		if i < limit {
			queries = append(queries, e.Query)
		}
		i++
	})
	if len(queries) == 0 {
		b.Skip("no queries")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for _, q := range queries {
			_ = ExtractConditions(q)
		}
	}
}

func pct(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
