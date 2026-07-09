package eql

// ExpectedCondition is a condition signature used for differential testing.
// It captures the fields the generator can predict deterministically.
type ExpectedCondition struct {
	Field        string   `json:"field"`
	Operator     string   `json:"operator"`
	Value        string   `json:"value,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
	Negated      bool     `json:"negated,omitempty"`
	SequenceStep int      `json:"sequence_step,omitempty"`
}

// GeneratedCase is one entry in the generated differential corpus: a rendered
// query plus the conditions a correct extractor must produce.
type GeneratedCase struct {
	ID       int64               `json:"id"`
	Seed     int64               `json:"seed"`
	Query    string              `json:"query"`
	Expected []ExpectedCondition `json:"expected"`
}
