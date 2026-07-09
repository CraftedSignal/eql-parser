package eql

import "testing"

var benchQueries = []string{
	`process where process.name : "cmd.exe" and process.parent.name : "explorer.exe"`,
	`process where process.name in ("a.exe", "b.exe", "c.exe", "d.exe", "e.exe")`,
	`sequence by process.entity_id with maxspan=5m [process where event.type == "start" and process.name : "msxsl.exe"] [network where event.type == "connection" and network.direction : "egress"]`,
	`process where not (a : "x" or b : "y") and cidrMatch(source.ip, "10.0.0.0/8") | head 100`,
}

func BenchmarkLexer(b *testing.B) {
	q := benchQueries[2]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = newLexer(q)
	}
}

func BenchmarkParse(b *testing.B) {
	q := benchQueries[2]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = Parse(q)
	}
}

func BenchmarkExtractConditions(b *testing.B) {
	for _, q := range benchQueries {
		b.Run(truncate(q, 32), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = ExtractConditions(q)
			}
		})
	}
}

func BenchmarkExtractSequence(b *testing.B) {
	q := benchQueries[2]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ExtractConditions(q)
	}
}
