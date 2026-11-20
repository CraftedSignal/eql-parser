package eql

import "testing"

// A pipe argument uses a field only when it names the whole dotted path, as
// ClassifyFieldProvenance already decides with fieldInText.
func TestClassifyFieldUsagePipesMatchWholePaths(t *testing.T) {
	res := ExtractConditions(`process where name == "x" | unique process.name, pid`)
	requireNoErrors(t, res)
	for field, want := range map[string]bool{
		"process.name": true,
		"pid":          true,
		"name":         false, // only a suffix of process.name
		"id":           false, // only a suffix of pid
		"process":      false, // only a prefix of process.name
		"":             false,
	} {
		if got := ClassifyFieldUsage(res, field).InPipes; got != want {
			t.Errorf("ClassifyFieldUsage(%q).InPipes = %v, want %v", field, got, want)
		}
	}
}
