package eql

import (
	"encoding/json"
	"testing"
)

func TestMaxSpanIntegerMillisecondsExact(t *testing.T) {
	for _, tc := range []struct {
		span string
		want int64
		ok   bool
	}{
		{"9007199254740991ms", 9007199254740991, true},
		{"9007199254740992ms", 9007199254740992, true},
		{"9007199254740993ms", 9007199254740993, true},
		{"9007199254740994ms", 9007199254740994, true},
		{"9223372036854775806ms", 9223372036854775806, true},
		{"9223372036854775807ms", 9223372036854775807, true},
		{"9223372036854775808ms", 0, false},
		{"999999999999999999999999999ms", 0, false},
		{"9007199254740993MS", 9007199254740993, true},
		{"0009007199254740993mS", 9007199254740993, true},
		{"0ms", 0, true},
		{"1.5ms", 1, true},
		{"0.5ms", 0, true},
		{"30s", 30000, true},
		{"1.5h", 5400000, true},
		{"1000micros", 1, true},
		{"1nanos", 0, true},
		{"3", 3000, true},
		{"106751991168d", 0, false},
		{"9223372036854776s", 0, false},
		{"", 0, false}, {"ms", 0, false}, {".ms", 0, false},
		{"1..0ms", 0, false}, {"1ems", 0, false}, {"-1ms", 0, false},
		{"+1ms", 0, false}, {"1 ms", 0, false}, {"1msx", 0, false},
	} {
		t.Run(tc.span, func(t *testing.T) {
			if got, ok := MaxSpanDuration(tc.span); got != tc.want || ok != tc.ok {
				t.Fatalf("MaxSpanDuration(%q)=(%d,%v), want (%d,%v)", tc.span, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestMaxSpanIntegerMillisecondsPublicProjection(t *testing.T) {
	for _, tc := range []struct {
		span       string
		want       int64
		jsonNumber string
	}{
		{"9007199254740991ms", 9007199254740991, "9007199254740991"},
		{"9007199254740992ms", 9007199254740992, "9007199254740992"},
		{"9007199254740993ms", 9007199254740993, "9007199254740993"},
		{"9007199254740994ms", 9007199254740994, "9007199254740994"},
		{"9223372036854775807ms", 9223372036854775807, "9223372036854775807"},
		{"9223372036854775808ms", 0, ""},
		{"9007199254740993MS", 9007199254740993, "9007199254740993"},
		{"0009007199254740993ms", 9007199254740993, "9007199254740993"},
		{"30s", 30000, "30000"}, {"1.5h", 5400000, "5400000"},
		{"1000micros", 1, "1"}, {"99999999999999999999d", 0, ""},
	} {
		t.Run(tc.span, func(t *testing.T) {
			query := `sequence with maxspan=` + tc.span + ` [a where x == 1] [b where y == 2]`
			extracted := ExtractConditions(query)
			analyzed := AnalyzeQuery(query)
			if len(extracted.Errors) != 0 || len(analyzed.Errors) != 0 || extracted.Sequence == nil || analyzed.Sequence == nil {
				t.Fatalf("public parse: %+v / %+v", extracted, analyzed)
			}
			if extracted.Sequence.MaxSpan != tc.span || analyzed.Sequence.MaxSpan != tc.span || extracted.Sequence.MaxSpanMS != tc.want || analyzed.Sequence.MaxSpanMS != tc.want {
				t.Fatalf("raw/exact derived projection: %+v / %+v", extracted.Sequence, analyzed.Sequence)
			}
			for _, v := range []any{extracted.Sequence, analyzed.Sequence} {
				data, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if got := string(fields["max_span_ms"]); got != tc.jsonNumber {
					t.Fatalf("encoded exact/omitted numeric field %q, want %q in %s", got, tc.jsonNumber, data)
				}
				if string(fields["max_span"]) != `"`+tc.span+`"` {
					t.Fatalf("raw maxspan not retained: %s", data)
				}
			}
		})
	}
}
