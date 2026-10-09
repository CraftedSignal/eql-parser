package eql

import (
	"strings"
)

// NormalizeQuery cleans up EQL text that was pasted from documents, extracted
// from JSON/YAML/TOML rule files, or otherwise mangled in transit, without
// changing the meaning of well-formed queries. ExtractConditions applies it
// automatically; Parse does not.
func NormalizeQuery(query string) string {
	q := query

	// Strip UTF-8 BOM.
	q = strings.TrimPrefix(q, "\ufeff")

	// Replace typographic characters that word processors substitute, but
	// only outside string literals: a value may hold an en dash (a Windows
	// flag variant) or a curly quote that it must match as written.
	q = outsideStringLiterals(q, typographicReplacer.Replace)

	// Strip markdown code fences: ```eql ... ``` or ``` ... ```.
	q = stripCodeFences(q)

	// Convert literal \n / \r\n / \t escape sequences outside string
	// literals into real whitespace (queries extracted from JSON often
	// carry them).
	q = decodeEscapedWhitespace(q)

	// Drop trailing semicolons (saved-query artifacts).
	q = strings.TrimRight(q, " \t\r\n")
	for strings.HasSuffix(q, ";") {
		q = strings.TrimRight(strings.TrimSuffix(q, ";"), " \t\r\n")
	}

	return strings.TrimSpace(q)
}

func stripCodeFences(q string) string {
	trimmed := strings.TrimSpace(q)
	if !strings.HasPrefix(trimmed, "```") {
		return q
	}
	lines := strings.Split(trimmed, "\n")
	// Drop the opening fence line (with its optional language tag); trimmed is
	// already known to start with ```. Since trimmed has no trailing blank
	// lines, the closing fence is the last line when present.
	lines = lines[1:]
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// decodeEscapedWhitespace converts literal backslash-n / backslash-r /
// backslash-t sequences that appear outside string literals into real
// whitespace. Inside strings they are legitimate escapes and left alone.
func decodeEscapedWhitespace(q string) string {
	if !strings.Contains(q, `\n`) && !strings.Contains(q, `\r`) && !strings.Contains(q, `\t`) {
		return q
	}
	var b strings.Builder
	b.Grow(len(q))
	i := 0
	for i < len(q) {
		c := q[i]

		// Skip over comments verbatim.
		if c == '/' && i+1 < len(q) && q[i+1] == '/' {
			for i < len(q) && q[i] != '\n' {
				b.WriteByte(q[i])
				i++
			}
			continue
		}

		// Copy string literals verbatim (handles ", ', """ and raw forms).
		if c == '"' || c == '\'' {
			end := scanStringEnd(q, i)
			b.WriteString(q[i:end])
			i = end
			continue
		}

		if c == '\\' && i+1 < len(q) {
			switch q[i+1] {
			case 'n':
				b.WriteByte('\n')
				i += 2
				continue
			case 'r':
				b.WriteByte('\r')
				i += 2
				continue
			case 't':
				b.WriteByte('\t')
				i += 2
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// scanStringEnd returns the index just past the string literal starting at
// position i (which must be a quote character).
func scanStringEnd(q string, i int) int {
	quote := q[i]
	// Triple-quoted raw string.
	if quote == '"' && i+2 < len(q) && q[i+1] == '"' && q[i+2] == '"' {
		j := i + 3
		for j+2 < len(q) {
			if q[j] == '"' && q[j+1] == '"' && q[j+2] == '"' {
				return j + 3
			}
			j++
		}
		return len(q)
	}
	j := i + 1
	for j < len(q) {
		if q[j] == '\\' && j+1 < len(q) {
			j += 2
			continue
		}
		if q[j] == quote {
			return j + 1
		}
		if q[j] == '\n' {
			// EQL strings are single-line; treat as unterminated.
			return j
		}
		j++
	}
	return len(q)
}

var typographicReplacer = strings.NewReplacer(
	"\u201c", `"`, "\u201d", `"`, "\u201e", `"`, "\u201f", `"`, // curly double quotes
	"\u2018", `'`, "\u2019", `'`, "\u201a", `'`, "\u201b", `'`, // curly single quotes
	"\u00ab", `"`, "\u00bb", `"`, // guillemets
	"\u2013", "-", "\u2014", "-", "\u2212", "-", // en/em dash, minus sign
	"\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "", "\u2060", "", // zero-width chars
	"\u00a0", " ", "\u202f", " ", "\u2007", " ", // non-breaking spaces
)

// outsideStringLiterals applies fix to the query text between string
// literals and keeps the literals as written. A curly quote that fix turns
// into a straight quote then opens or closes a literal as usual.
func outsideStringLiterals(q string, fix func(string) string) string {
	var b strings.Builder
	b.Grow(len(q))
	start := 0
	for i := 0; i < len(q); {
		if q[i] != '"' && q[i] != '\'' {
			i++
			continue
		}
		b.WriteString(fix(q[start:i]))
		end := scanStringEnd(q, i)
		b.WriteString(q[i:end])
		start, i = end, end
	}
	b.WriteString(fix(q[start:]))
	return b.String()
}
