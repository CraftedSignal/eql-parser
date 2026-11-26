package eql

import (
	"encoding/json"
	"testing"
)

// Integer unit projections must not pass through float64. These expectations
// are exact decimal integers; no backend execution or decimal EQL support is claimed.
func TestMaxSpanIntegerUnitsExact(t *testing.T) {
	cases := []struct {
		span   string
		want   int64
		ok     bool
		public bool
	}{
		{"72057594037929s", 72057594037929000, true, true},
		{"72057594037928s", 72057594037928000, true, true},
		{"72057594037930s", 72057594037930000, true, true},
		{"9223372036854775807ms", 9223372036854775807, true, true},
		{"9223372036854775808ms", 0, false, true},
		{"9223372036854775s", 9223372036854775000, true, true},
		{"9223372036854776s", 0, false, true},
		{"153722867280912m", 9223372036854720000, true, true},
		{"153722867280913m", 0, false, true},
		{"2562047788015h", 9223372036854000000, true, true},
		{"2562047788016h", 0, false, true},
		{"106751991167d", 9223372036828800000, true, true},
		{"106751991168d", 0, false, true},
		{"9223372036854775sec", 9223372036854775000, true, true},
		{"9223372036854775secs", 9223372036854775000, true, true},
		{"9223372036854775second", 9223372036854775000, true, true},
		{"9223372036854775seconds", 9223372036854775000, true, true},
		{"153722867280912min", 9223372036854720000, true, true},
		{"153722867280912mins", 9223372036854720000, true, true},
		{"153722867280912minute", 9223372036854720000, true, true},
		{"153722867280912minutes", 9223372036854720000, true, true},
		{"2562047788015hour", 9223372036854000000, true, true},
		{"2562047788015hours", 9223372036854000000, true, true},
		{"106751991167day", 9223372036828800000, true, true},
		{"106751991167days", 9223372036828800000, true, true},
		{"00072057594037929S", 72057594037929000, true, true},
		{"000153722867280912MiNuTeS", 9223372036854720000, true, true},
		{"2562047788015HOURS", 9223372036854000000, true, true},
		{"9007199254740993ms", 9007199254740993, true, true},
		{"0009007199254740993mS", 9007199254740993, true, true},
		{"0ms", 0, true, false},
		{"0s", 0, true, false},
		{"0m", 0, true, false},
		{"0h", 0, true, false},
		{"0d", 0, true, false},
		{"1.5s", 1500, true, true},
		{"1.5h", 5400000, true, true},
		{"1.5ms", 1, true, true},
		{"1e3s", 1000000, true, true},
		{"1e-3s", 1, true, true},
		{"3.3733E+15ms", 3373300000000000, true, true},
		{"0.5ms", 0, true, true},
		{"1000micros", 1, true, true},
		{"1nanos", 0, true, true},
		{"3", 3000, true, false},
		{"72057594037929", 72057594037928992, true, false},
		{"", 0, false, false},
		{"ms", 0, false, false},
		{"1..0s", 0, false, false},
		{"1e+s", 0, false, false},
		{"1e--3s", 0, false, false},
		{"1e19s", 0, false, false},
		{"-1s", 0, false, false},
		{"+1s", 0, false, false},
		{"1 s", 0, false, false},
		{"1sx", 0, false, false},
		{"18446744073709551616s", 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.span, func(t *testing.T) {
			got, ok := MaxSpanDuration(tc.span)
			if got != tc.want || ok != tc.ok {
				t.Errorf("MaxSpanDuration(%q)=(%d,%v), want (%d,%v)", tc.span, got, ok, tc.want, tc.ok)
			}
			if !tc.public {
				return
			}
			query := "sequence with maxspan=" + tc.span + " [process where true] [network where true]"
			extracted := ExtractConditions(query)
			analyzed := AnalyzeQuery(query)
			if len(extracted.Errors) != 0 || len(analyzed.Errors) != 0 || extracted.Sequence == nil || analyzed.Sequence == nil {
				t.Fatalf("public parse: %+v / %+v", extracted, analyzed)
			}
			if extracted.Sequence.MaxSpan != tc.span || analyzed.Sequence.MaxSpan != tc.span || extracted.Sequence.MaxSpanMS != tc.want || analyzed.Sequence.MaxSpanMS != tc.want {
				t.Errorf("public exact/raw projection: %+v / %+v, want %q and %d", extracted.Sequence, analyzed.Sequence, tc.span, tc.want)
			}
			for _, sequence := range []any{extracted.Sequence, analyzed.Sequence} {
				data, err := json.Marshal(sequence)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				wantNumber := ""
				if tc.ok && tc.want != 0 {
					encoded, err := json.Marshal(tc.want)
					if err != nil {
						t.Fatal(err)
					}
					wantNumber = string(encoded)
				}
				if string(fields["max_span_ms"]) != wantNumber {
					t.Errorf("exact/omitted numeric JSON = %q, want %q in %s", fields["max_span_ms"], wantNumber, data)
				}
				wantRaw, err := json.Marshal(tc.span)
				if err != nil {
					t.Fatal(err)
				}
				if string(fields["max_span"]) != string(wantRaw) {
					t.Errorf("raw span lost: %s", data)
				}
			}
		})
	}
}
