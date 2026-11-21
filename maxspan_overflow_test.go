package eql

import "testing"

// A maxspan beyond the int64 range of milliseconds is not a duration this
// library can return; converting it would overflow (to a negative value on
// amd64, and differently on other architectures).
func TestMaxSpanDurationOutOfRange(t *testing.T) {
	if ms, ok := MaxSpanDuration("106751991167d"); !ok || ms <= 0 {
		t.Errorf("MaxSpanDuration(106751991167d) = (%d, %v), want the largest day count in range", ms, ok)
	}
	for _, span := range []string{"106751991168d", "99999999999999999999d", "9223372036854776s"} {
		if ms, ok := MaxSpanDuration(span); ok || ms != 0 {
			t.Errorf("MaxSpanDuration(%s) = (%d, %v), want (0, false)", span, ms, ok)
		}
	}
	res := ExtractConditions(`sequence with maxspan=99999999999999999999d [a where x == 1] [b where y == 2]`)
	requireNoErrors(t, res)
	if res.Sequence.MaxSpan != "99999999999999999999d" || res.Sequence.MaxSpanMS != 0 {
		t.Errorf("Sequence maxspan = %q, %d ms; want the text kept and no milliseconds", res.Sequence.MaxSpan, res.Sequence.MaxSpanMS)
	}
}
