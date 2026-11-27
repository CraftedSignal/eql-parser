package eql

import (
	"strings"
)

// NormalizeQuery cleans up EQL text that was pasted from documents, extracted
// from JSON/YAML/TOML rule files, or otherwise mangled in transit, without
// changing the meaning of well-formed queries. ExtractConditions applies it
// automatically; Parse does not.
func NormalizeQuery(query string) string {
	return normalizeQueryWithBudget(query, nil)
}

func normalizeQueryWithBudget(query string, budget *parseBudget) string {
	q := query

	// Strip UTF-8 BOM.
	q = strings.TrimPrefix(q, "\ufeff")

	// Remove saved-query fences before protecting backtick identifiers.
	q = stripCodeFences(q)

	// Replace typographic characters that word processors substitute, but
	// only outside string literals and escaped identifiers: both may hold
	// typographic characters that must remain distinct from ASCII spelling.
	q = outsideStringLiterals(q, typographicReplacer.Replace, budget)

	// Convert literal \n / \r\n / \r / \t escape sequences outside string
	// literals into real whitespace (queries extracted from JSON often
	// carry them).
	q = decodeEscapedWhitespace(q, budget)

	// Drop trailing semicolons (saved-query artifacts).
	q = strings.TrimRight(q, " \t\r\n")
	for strings.HasSuffix(q, ";") {
		q = strings.TrimRight(strings.TrimSuffix(q, ";"), " \t\r\n")
	}

	return strings.TrimSpace(q)
}

func stripCodeFences(q string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(q, "\r\n", "\n"), "\r", "\n"))
	// Typography repair can also make outer artifact-only lines empty. Peel
	// those lines only from the detection view; nonfenced input still returns q.
	for trimmed != "" {
		line, rest, found := strings.Cut(trimmed, "\n")
		if strings.TrimSpace(typographicReplacer.Replace(line)) != "" {
			break
		}
		if !found {
			return q
		}
		trimmed = rest
	}
	for trimmed != "" {
		lastBreak := strings.LastIndexByte(trimmed, '\n')
		if strings.TrimSpace(typographicReplacer.Replace(trimmed[lastBreak+1:])) != "" {
			break
		}
		if lastBreak < 0 {
			return q
		}
		trimmed = trimmed[:lastBreak]
	}
	opening, _, _ := strings.Cut(trimmed, "\n")
	// Fence recognition historically followed typography repair. Apply that
	// repair only to boundary detection here, keeping interior query bytes raw.
	if !strings.HasPrefix(strings.TrimSpace(typographicReplacer.Replace(opening)), "```") {
		return q
	}
	lines := strings.Split(trimmed, "\n")
	// Drop the opening fence line (with its optional language tag); trimmed is
	// already known to start with ```. Since trimmed has no trailing blank
	// lines, the closing fence is the last line when present.
	lines = lines[1:]
	if len(lines) > 0 && strings.TrimSpace(typographicReplacer.Replace(lines[len(lines)-1])) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// decodeEscapedWhitespace converts literal backslash-n / backslash-r /
// backslash-t sequences that appear outside string literals and escaped
// identifiers into real whitespace. Protected spans and comments are copied
// as written (see commentEnd).

func decodeEscapedWhitespace(q string, budget *parseBudget) string {
	if !strings.Contains(q, `\n`) && !strings.Contains(q, `\r`) && !strings.Contains(q, `\t`) {
		return q
	}
	escapedLines := !strings.ContainsAny(q, "\r\n")
	var b strings.Builder
	b.Grow(len(q))
	i := 0
	for i < len(q) {
		c := q[i]
		if c == '`' {
			end := scanBacktickEnd(q, i)
			b.WriteString(q[i:end])
			i = end
			continue
		}

		// Skip over comments verbatim.
		if end := commentEnd(q, i, escapedLines, budget); end > i {
			b.WriteString(q[i:end])
			i = end
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

// scanBacktickEnd follows the lexer: doubled backticks are identifier content,
// a single backtick closes it, and an unterminated span extends to EOF.
func scanBacktickEnd(q string, i int) int {
	for j := i + 1; j < len(q); j++ {
		if q[j] != '`' {
			continue
		}
		if j+1 < len(q) && q[j+1] == '`' {
			j++
			continue
		}
		return j + 1
	}
	return len(q)
}

// scanStringEnd returns the index just past the string literal starting at
// position i (which must be a quote character).
func scanStringEnd(q string, i int) int {
	quote := q[i]
	// Triple-quoted raw string. As in the lexer, up to two more quotes after
	// the closing """ belong to it.
	if quote == '"' && i+2 < len(q) && q[i+1] == '"' && q[i+2] == '"' {
		j := i + 3
		for j+2 < len(q) {
			if q[j] == '"' && q[j+1] == '"' && q[j+2] == '"' {
				end := j + 3
				for end < len(q) && end < j+5 && q[end] == '"' {
					end++
				}
				return end
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
		if q[j] == '\r' || q[j] == '\n' {
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
// literals and escaped identifiers and keeps those spans as written. A curly quote that fix turns
// into a straight quote then opens or closes a literal as usual. A quote
// inside a comment opens no literal.

func outsideStringLiterals(q string, fix func(string) string, budget *parseBudget) string {
	escapedLines := !strings.ContainsAny(q, "\r\n")
	var b strings.Builder
	b.Grow(len(q))
	start := 0
	for i := 0; i < len(q); {
		if q[i] == '`' {
			b.WriteString(fix(q[start:i]))
			end := scanBacktickEnd(q, i)
			b.WriteString(q[i:end])
			start, i = end, end
			continue
		}
		if end := commentEnd(q, i, escapedLines, budget); end > i {
			i = end
			continue
		}
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

// commentEnd returns the index just past the comment that starts at q[i], or
// i when none starts there. As in the lexer, block comments nest and line
// comments end at the line break. A query with escapedLines has no line break
// of its own but carries them as literal \n or \r sequences (taken from JSON),
// so those end a line comment instead.

func commentEnd(q string, i int, escapedLines bool, budget *parseBudget) int {
	if q[i] != '/' || i+1 >= len(q) {
		return i
	}
	switch q[i+1] {
	case '*':
		depth := 1
		for j := i + 2; j < len(q); {
			// Normalization work was reserved by the caller; check only
			// cancellation here, without charging the scan twice.
			budget.check(0)
			switch {
			case j+1 < len(q) && q[j] == '/' && q[j+1] == '*':
				depth++
				j += 2
			case j+1 < len(q) && q[j] == '*' && q[j+1] == '/':
				depth--
				j += 2
				if depth == 0 {
					return j
				}
			default:
				j++
			}
		}
		return len(q)
	case '/':
		if escapedLines {
			end := strings.Index(q[i:], `\n`)
			if cr := strings.Index(q[i:], `\r`); cr >= 0 && (end < 0 || cr < end) {
				end = cr
			}
			if end >= 0 {
				return i + end
			}
			return len(q)
		}
		if end := strings.IndexAny(q[i:], "\r\n"); end >= 0 {
			return i + end
		}
		return len(q)
	}
	return i
}
