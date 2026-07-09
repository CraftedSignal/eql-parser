package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file generates complex but valid Sigma rules to pad the real corpus up
// to the 100k+ scale target. Generated rules exercise the full modifier set,
// value lists, multiple selections, negation, and quantifier conditions
// (`all of`, `1 of selection_*`), so the parser and the Sigma->EQL translator
// are stress-tested on realistic structure, not just trivial rules.

var (
	sgCategories = []struct{ category, product string }{
		{"process_creation", "windows"}, {"network_connection", "windows"},
		{"file_event", "windows"}, {"registry_set", "windows"},
		{"image_load", "windows"}, {"dns_query", "windows"},
		{"process_creation", "linux"}, {"file_event", "linux"},
		{"proxy", ""}, {"webserver", ""}, {"firewall", ""},
	}
	sgFields = []string{
		"Image", "CommandLine", "ParentImage", "ParentCommandLine", "OriginalFileName",
		"TargetFilename", "User", "IntegrityLevel", "TargetObject", "Details",
		"ImageLoaded", "Signature", "ServiceName", "QueryName", "DestinationHostname",
		"CurrentDirectory", "ParentUser", "LogonId", "Product", "Company", "Description",
	}
	sgNumericFields = []string{"DestinationPort", "SourcePort", "EventID", "ProcessId"}
	sgStringMods    = []string{"", "|contains", "|startswith", "|endswith", "|contains|all"}
	sgValues        = []string{
		`\mimikatz.exe`, `\powershell.exe`, `\cmd.exe`, `\rundll32.exe`, `\regsvr32.exe`,
		`sekurlsa::`, `-enc `, `-ExecutionPolicy Bypass`, `DownloadString`, `/etc/passwd`,
		`C:\Windows\Temp\`, `\Device\HarddiskVolume`, `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run`,
		`.dll`, `.scr`, `.hta`, `evil.com`, `powershell -nop -w hidden`,
	}
)

func sgPick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

// generateSigmaRule renders one complex, valid Sigma rule as YAML text.
func generateSigmaRule(rng *rand.Rand, id int) string {
	cat := sgCategories[rng.Intn(len(sgCategories))]

	// Build 1-4 named selections.
	nSel := 1 + rng.Intn(4)
	detection := map[string]any{}
	var selNames []string
	for i := 0; i < nSel; i++ {
		name := fmt.Sprintf("selection_%d", i)
		if i > 0 && rng.Intn(3) == 0 {
			name = fmt.Sprintf("filter_%d", i)
		}
		selNames = append(selNames, name)
		detection[name] = generateSelection(rng)
	}

	detection["condition"] = generateCondition(rng, selNames)
	if rng.Intn(4) == 0 {
		detection["timeframe"] = fmt.Sprintf("%dm", 1+rng.Intn(60))
	}

	rule := map[string]any{
		"title":  fmt.Sprintf("Generated Complex Rule %d", id),
		"id":     fmt.Sprintf("00000000-0000-0000-0000-%012d", id),
		"status": sgPick(rng, []string{"experimental", "test", "stable"}),
		"logsource": func() map[string]any {
			ls := map[string]any{"category": cat.category}
			if cat.product != "" {
				ls["product"] = cat.product
			}
			return ls
		}(),
		"detection": detection,
		"level":     sgPick(rng, []string{"low", "medium", "high", "critical"}),
	}

	out, err := yaml.Marshal(rule)
	if err != nil {
		return ""
	}
	return string(out)
}

// generateSelection builds one selection map: 1-4 field entries, each a
// modified field key mapped to a scalar or a value list.
func generateSelection(rng *rand.Rand) map[string]any {
	sel := map[string]any{}
	n := 1 + rng.Intn(4)
	used := map[string]bool{}
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0, 1:
			// Numeric comparison.
			f := sgPick(rng, sgNumericFields)
			mod := sgPick(rng, []string{"", "|gt", "|lt", "|gte", "|lte"})
			key := f + mod
			if used[key] {
				continue
			}
			used[key] = true
			sel[key] = rng.Intn(65536)
		case 2:
			// CIDR match.
			key := "DestinationIp|cidr"
			if used[key] {
				continue
			}
			used[key] = true
			sel[key] = sgPick(rng, []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12", "0.0.0.0/0"})
		case 3:
			// Regex.
			f := sgPick(rng, sgFields)
			key := f + "|re"
			if used[key] {
				continue
			}
			used[key] = true
			sel[key] = sgPick(rng, []string{`.*\\[a-z]{4,}\.exe`, `(?i)invoke-\w+`, `\d{1,3}(\.\d{1,3}){3}`})
		default:
			// String match, scalar or list.
			f := sgPick(rng, sgFields)
			mod := sgPick(rng, sgStringMods)
			key := f + mod
			if used[key] {
				continue
			}
			used[key] = true
			if rng.Intn(2) == 0 {
				sel[key] = sgPick(rng, sgValues)
			} else {
				k := 2 + rng.Intn(4)
				vals := make([]any, k)
				for j := 0; j < k; j++ {
					vals[j] = sgPick(rng, sgValues)
				}
				sel[key] = vals
			}
		}
	}
	// Guarantee at least one entry.
	if len(sel) == 0 {
		sel[sgPick(rng, sgFields)+"|contains"] = sgPick(rng, sgValues)
	}
	return sel
}

// generateCondition builds a valid Sigma condition expression referencing the
// given selection names, with realistic complexity.
func generateCondition(rng *rand.Rand, names []string) string {
	positives := names[:0:0]
	var filters []string
	for _, n := range names {
		if strings.HasPrefix(n, "filter") {
			filters = append(filters, n)
		} else {
			positives = append(positives, n)
		}
	}
	if len(positives) == 0 {
		positives = names
		filters = nil
	}

	var base string
	switch rng.Intn(6) {
	case 0:
		base = strings.Join(positives, " and ")
	case 1:
		base = strings.Join(positives, " or ")
	case 2:
		if len(positives) >= 2 {
			base = "(" + positives[0] + " and " + positives[1] + ")"
			for _, p := range positives[2:] {
				base += " or " + p
			}
		} else {
			base = positives[0]
		}
	case 3:
		base = "all of selection_*"
	case 4:
		base = "1 of selection_*"
	default:
		base = "all of them"
		return base // "all of them" already includes filters
	}

	for _, f := range filters {
		if rng.Intn(2) == 0 {
			base += " and not " + f
		}
	}
	return base
}

// generateComplexRulesInto writes n generated Sigma rules as JSONL entries to
// the encoder. Returns the count written.
func generateComplexRulesInto(enc interface{ Encode(any) error }, n int, seed int64, startID int) (int, error) {
	rng := rand.New(rand.NewSource(seed))
	written := 0
	for i := 0; i < n; i++ {
		rule := generateSigmaRule(rng, startID+i)
		if rule == "" {
			continue
		}
		if err := enc.Encode(sigmaRuleEntry{
			Source: "generated",
			Path:   "gen/" + strconv.Itoa(startID+i) + ".yml",
			Rule:   rule,
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// sigmaRuleEntry is one line of the rule corpus JSONL.
type sigmaRuleEntry struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Rule   string `json:"rule"`
}
