package eql

import "testing"

// A negated condition excludes events; it does not name their type.
func TestGetEventTypeIgnoresNegatedConditions(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`process where not event.code == "4688"`, ""},
		{`process where not event.code : ("4688", "4689")`, ""},
		{`process where event.code not in ("4688", "4689")`, ""},
		{`process where event.code == "4688" and not event.provider == "Microsoft-Windows-Sysmon"`, "windows_4688"},
		{`process where not event.code == "4689" and event.code == "4688"`, "windows_4688"},
		{`process where event.code == "1" and not user.name == "x" and event.provider : "Microsoft-Windows-Sysmon"`, "sysmon_1"},
	} {
		if got := GetEventTypeFromConditions(ExtractConditions(tc.query)); got != tc.want {
			t.Errorf("GetEventTypeFromConditions(%s) = %q, want %q", tc.query, got, tc.want)
		}
	}
}
