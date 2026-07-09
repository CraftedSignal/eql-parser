package eql

import (
	"strings"
	"testing"
)

// Real-world EQL queries modeled on Elastic Security detection rules
// (github.com/elastic/detection-rules), the EQL syntax reference, and legacy
// Endgame eqllib rules. Each entry asserts on properties the parser must get
// right, not just that it doesn't crash.

type rwCase struct {
	name  string
	query string
	check func(t *testing.T, r *ParseResult)
}

// expectClean asserts the query parsed with no errors.
func expectClean(t *testing.T, r *ParseResult) {
	t.Helper()
	if len(r.Errors) > 0 {
		t.Errorf("unexpected errors: %v", r.Errors)
	}
}

func hasCond(r *ParseResult, field, op string) bool {
	for _, c := range r.Conditions {
		if c.Field == field && c.Operator == op {
			return true
		}
	}
	return false
}

func hasField(r *ParseResult, field string) bool {
	for _, f := range r.Fields {
		if f == field {
			return true
		}
	}
	return false
}

func condValues(r *ParseResult, field string) []string {
	for _, c := range r.Conditions {
		if c.Field == field {
			if len(c.Alternatives) > 0 {
				return c.Alternatives
			}
			return []string{c.Value}
		}
	}
	return nil
}

var realWorldCases = []rwCase{
	// ---- single-event process rules ----
	{
		name:  "masquerading_regsvr32",
		query: `process where event.type == "start" and process.name : "regsvr32.exe" and process.parent.name : "mshta.exe"`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.EventCategories[0] != "process" {
				t.Errorf("category = %v", r.EventCategories)
			}
			if !hasCond(r, "process.name", ":") || !hasCond(r, "process.parent.name", ":") {
				t.Errorf("conditions = %+v", r.Conditions)
			}
		},
	},
	{
		name:  "suspicious_powershell_encoded",
		query: `process where process.name : "powershell.exe" and process.args : ("-enc", "-EncodedCommand", "-e")`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			vals := condValues(r, "process.args")
			if len(vals) != 3 {
				t.Errorf("args alternatives = %v", vals)
			}
		},
	},
	{
		name:  "unsigned_process_from_temp",
		query: `process where event.type == "start" and process.code_signature.trusted == false and process.executable : "C:\\Users\\*\\AppData\\Local\\Temp\\*"`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if !hasCond(r, "process.code_signature.trusted", "==") {
				t.Error("missing code_signature.trusted")
			}
		},
	},
	{
		name:  "lolbin_rundll32_no_args",
		query: `process where process.name : "rundll32.exe" and process.args_count == 1`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if !hasCond(r, "process.args_count", "==") {
				t.Error("missing args_count")
			}
		},
	},
	{
		name:  "renamed_utility_original_filename",
		query: `process where process.pe.original_file_name in ("PsExec.c", "procdump", "Cain.exe") and not process.name : ("PsExec64.exe", "procdump.exe")`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			for _, c := range r.Conditions {
				if c.Field == "process.name" && !c.Negated {
					t.Error("process.name should be negated")
				}
			}
		},
	},
	{
		name:  "wmiprvse_child_process",
		query: `process where event.type == "start" and process.parent.name : "wmiprvse.exe" and process.name : ("powershell.exe", "cmd.exe", "wscript.exe", "cscript.exe")`,
		check: expectCleanOnly,
	},

	// ---- library / registry / file categories ----
	{
		name:  "image_load_unsigned_dll",
		query: `library where dll.name : "*.dll" and dll.code_signature.trusted == false`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.EventCategories[0] != "library" {
				t.Errorf("category = %v", r.EventCategories)
			}
		},
	},
	{
		name:  "registry_run_key_persistence",
		query: `registry where registry.path : ("HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Run\\*", "HKEY_USERS\\*\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Run\\*")`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if len(condValues(r, "registry.path")) != 2 {
				t.Errorf("values = %v", condValues(r, "registry.path"))
			}
		},
	},
	{
		name:  "file_creation_startup_folder",
		query: `file where event.type != "deletion" and file.path : "C:\\*\\Start Menu\\Programs\\Startup\\*"`,
		check: expectCleanOnly,
	},
	{
		name:  "sensitive_file_access_optional",
		query: `file where event.action == "open" and ?user.id != null and file.path : "/etc/shadow"`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			for _, c := range r.Conditions {
				if c.Field == "user.id" && !c.IsOptional {
					t.Error("user.id should be optional")
				}
			}
		},
	},

	// ---- network rules ----
	{
		name:  "outbound_to_rare_port",
		query: `network where network.direction : ("outgoing", "egress") and destination.port not in (80, 443, 53, 8080)`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			for _, c := range r.Conditions {
				if c.Field == "destination.port" && !c.Negated {
					t.Error("port should be negated")
				}
			}
		},
	},
	{
		name:  "rdp_from_internet",
		query: `network where destination.port == 3389 and not cidrMatch(source.ip, "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			found := false
			for _, c := range r.Conditions {
				if c.Field == "source.ip" && c.Function == "cidrMatch" && c.Negated {
					found = true
				}
			}
			if !found {
				t.Errorf("cidrMatch not extracted correctly: %+v", r.Conditions)
			}
		},
	},
	{
		name:  "dns_tunneling_long_query",
		query: `network where event.type == "protocol" and network.protocol == "dns" and length(dns.question.name) > 100`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			for _, c := range r.Conditions {
				if c.Field == "dns.question.name" && c.Function != "length" {
					t.Errorf("expected length function, got %q", c.Function)
				}
			}
		},
	},

	// ---- sequences (the heart of EQL) ----
	{
		name: "msxsl_network_after_start",
		query: `sequence by process.entity_id
		  [process where event.type == "start" and process.name : "msxsl.exe"]
		  [network where event.type == "connection" and network.direction : "egress"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence == nil || r.Sequence.Kind != "sequence" {
				t.Fatalf("sequence = %+v", r.Sequence)
			}
			if len(r.Sequence.ByFields) != 1 || r.Sequence.ByFields[0] != "process.entity_id" {
				t.Errorf("by = %v", r.Sequence.ByFields)
			}
			if len(r.Sequence.Steps) != 2 {
				t.Errorf("steps = %d", len(r.Sequence.Steps))
			}
		},
	},
	{
		name: "usb_exec_then_network",
		query: `sequence by process.entity_id with maxspan=5m
		  [process where host.os.type == "windows" and event.action == "start" and
		    (process.Ext.device.bus_type : "usb" or process.Ext.device.product_id : "USB *") and
		    (process.code_signature.trusted == false or process.code_signature.exists == false)]
		  [network where host.os.type == "windows" and event.action == "connection_attempted"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence.MaxSpanMS != 300000 {
				t.Errorf("maxspan = %d ms", r.Sequence.MaxSpanMS)
			}
		},
	},
	{
		// Per-step join keys of matching arity: correlate a child process to
		// its parent across the sequence. global(1) + step(1) on each side.
		name: "credential_dumping_lsass",
		query: `sequence by host.id with maxspan=1m
		  [process where process.name : "rundll32.exe"] by process.entity_id
		  [process where process.name : "lsass.exe"] by process.parent.entity_id`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if len(r.Sequence.ByFields) != 1 {
				t.Errorf("global by = %v", r.Sequence.ByFields)
			}
			if len(r.Sequence.Steps[0].ByFields) != 1 || len(r.Sequence.Steps[1].ByFields) != 1 {
				t.Errorf("per-step by = %+v", r.Sequence.Steps)
			}
		},
	},
	{
		name: "brute_force_then_success",
		query: `sequence by source.ip with maxspan=10s
		  [authentication where event.outcome == "failure"] with runs=5
		  [authentication where event.outcome == "success"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence.Steps[0].Runs != 5 {
				t.Errorf("runs = %d", r.Sequence.Steps[0].Runs)
			}
		},
	},
	{
		name: "process_no_termination_missing_event",
		query: `sequence by process.entity_id with maxspan=5m
		  [process where event.type == "start" and process.name : "payload.exe"]
		  ![process where event.type == "end"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if !r.Sequence.Steps[1].Missing {
				t.Error("second step should be a missing event")
			}
		},
	},
	{
		name: "sequence_until_process_end",
		query: `sequence by process.entity_id
		  [file where event.type == "creation"]
		  [network where event.type == "connection"]
		  until [process where event.type == "end"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence.Until == nil {
				t.Error("missing until")
			}
		},
	},
	{
		name: "join_by_user",
		query: `join by user.name
		  [authentication where event.outcome == "success"]
		  [process where process.name : "net.exe"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence.Kind != "join" {
				t.Errorf("kind = %s", r.Sequence.Kind)
			}
		},
	},
	{
		name: "sample_by_host",
		query: `sample by host.id
		  [process where process.name : "malware.exe"]
		  [file where file.name : "ransom.txt"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if r.Sequence.Kind != "sample" {
				t.Errorf("kind = %s", r.Sequence.Kind)
			}
		},
	},

	// ---- pipes ----
	{
		name:  "process_with_head_pipe",
		query: `process where process.name : "whoami.exe" | head 10`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if len(r.Pipes) != 1 || r.Pipes[0].Name != "head" {
				t.Errorf("pipes = %+v", r.Pipes)
			}
		},
	},
	{
		name:  "legacy_unique_count_pipe",
		query: `process where process.name == "cmd.exe" | unique process.parent.name | count`,
		check: expectCleanOnly,
	},

	// ---- legacy endgame forms ----
	{
		name:  "legacy_single_quotes",
		query: `process where process_name == 'net.exe' and command_line == '* group *'`,
		check: expectCleanOnly,
	},
	{
		name:  "legacy_wildcard_function",
		query: `process where wildcard(process.command_line, "*mimikatz*", "*sekurlsa*")`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			if !hasCond(r, "process.command_line", "wildcard") {
				t.Error("wildcard not extracted")
			}
		},
	},
	{
		name:  "legacy_child_of_lineage",
		query: `process where process.name == "cmd.exe" and child of [process where process.name == "explorer.exe"]`,
		check: func(t *testing.T, r *ParseResult) {
			expectClean(t, r)
			found := false
			for _, c := range r.Conditions {
				if c.Lineage == "child" {
					found = true
				}
			}
			if !found {
				t.Error("lineage not extracted")
			}
		},
	},
	{
		name:  "legacy_descendant_of",
		query: `network where descendant of [process where process.name == "powershell.exe"]`,
		check: expectCleanOnly,
	},
}

// expectCleanOnly is a check that only asserts a clean parse.
func expectCleanOnly(t *testing.T, r *ParseResult) {
	t.Helper()
	expectClean(t, r)
}

func TestRealWorldCases(t *testing.T) {
	for _, tc := range realWorldCases {
		t.Run(tc.name, func(t *testing.T) {
			r := ExtractConditions(tc.query)
			for i, c := range r.Conditions {
				if c.Field == "" || c.Operator == "" {
					t.Errorf("condition %d malformed: %+v", i, c)
				}
			}
			if tc.check != nil {
				tc.check(t, r)
			}
		})
	}
}

// realWorldQueries is a broad bank of queries exercised for parse-cleanliness
// and no-panic. These lean on the full surface of EQL syntax.
var realWorldQueries = []string{
	// Comparison + boolean logic
	`process where process.name : "cmd.exe" and (process.args : "*/c*" or process.args : "*/k*")`,
	`process where process.parent.name : "outlook.exe" and process.name : ("cmd.exe", "powershell.exe", "wscript.exe", "cscript.exe", "mshta.exe")`,
	`process where event.type == "start" and process.name : "svchost.exe" and not process.parent.name : "services.exe"`,
	`process where process.name : "net.exe" and process.args : "user" and process.args : ("/add", "/delete")`,
	// String predicates
	`process where startsWith(process.name, "wscript") and endsWith(process.command_line, ".vbs")`,
	`file where stringContains(file.path, "\\Temp\\") and file.extension in ("exe", "dll", "scr")`,
	`process where process.command_line regex~ """.*Base64.*FromBase64String.*"""`,
	`process where match(process.command_line, "(?i).*downloadstring.*")`,
	// Optional fields and null checks
	`process where ?process.Ext.token.integrity_level_name : "high" and process.name : "cmd.exe"`,
	`network where ?destination.geo.country_iso_code != null and destination.port == 4444`,
	`process where ?process.parent.name == null`,
	// Math
	`process where process.args_count > 5 and process.args_count < 20`,
	`network where source.bytes + destination.bytes > 1000000`,
	`process where (4 / process.thread.count) >= 1`,
	// Numeric literal forms
	`network where destination.port == 0x1BB or destination.port == 443`,
	`process where process.pid == 4 and process.ppid != 0`,
	// Sequences with varied shapes
	`sequence by host.id [process where event.action == "start"] [registry where event.action == "modification"] [network where event.action == "connection"]`,
	`sequence with maxspan=30s [file where event.type == "creation" and file.extension == "exe"] [process where event.type == "start"]`,
	`sequence by process.entity_id, user.name with maxspan=1h [process where true] [file where true] [network where true]`,
	`sequence by user.name with maxspan=5s [authentication where event.outcome : "failure"] with runs=10 [authentication where event.outcome : "success"]`,
	// Sample
	`sample by host.id [process where process.name : "a.exe"] [dns where dns.question.name : "evil.com"] [file where file.name : "x.dll"]`,
	// Pipes
	`process where process.name : "powershell.exe" | tail 100`,
	`authentication where event.outcome : "failure" | unique_count source.ip`,
	`process where true | filter process.parent.name : "services.exe" | head 50`,
	// Nested parentheses and negation
	`process where not (process.name : ("a.exe", "b.exe") and not (user.name : "admin" or user.name : "system"))`,
	`process where ((a == 1 and b == 2) or (c == 3 and d == 4)) and not e == 5`,
	// Backtick fields
	"process where `process name` : \"cmd.exe\"",
	"network where `destination-ip` != \"127.0.0.1\"",
	// Array indices
	`process where process.args[0] : "*\\python.exe" and process.args[1] : "*.py"`,
	// Case-insensitive everything
	`process where process.name in~ ("CMD.EXE", "PowerShell.EXE") or process.name like~ "*.SCR"`,
	// Unicode / RTL spoofing
	`file where file.name : "*\u{202e}*"`,
	`process where process.name : "*\u{200b}*"`,
	// Triple-quoted raw regex
	`registry where registry.data.strings regex """.*\\[a-z0-9]{16,}\.exe"""`,
	// event.category any
	`any where event.action : "user_login" and event.outcome : "failure"`,
	`any where true`,
	// Long OR chains that merge
	`process where process.name : "a.exe" or process.name : "b.exe" or process.name : "c.exe" or process.name : "d.exe"`,
	// Complex real detection: PsExec
	`process where event.type == "start" and (process.name : "PsExec.exe" or process.pe.original_file_name == "psexec.c") and not process.args : "-accepteula"`,
	// Kerberoasting
	`sequence by winlog.event_data.TargetUserName with maxspan=1m [authentication where event.code == "4769" and winlog.event_data.TicketEncryptionType == "0x17"]  [authentication where event.code == "4769"]`,
	// Suspicious parent-child with lineage (legacy)
	`process where process.name : "whoami.exe" and descendant of [process where process.name : "w3wp.exe"]`,
	// Defense evasion: clearing logs
	`process where process.name : "wevtutil.exe" and process.args : ("cl", "clear-log")`,
	// Scheduled task
	`process where process.name : "schtasks.exe" and process.args : "/create" and process.args : ("/ru", "system")`,
	// WMI persistence
	`process where process.name : "wmic.exe" and process.args : ("/namespace:*subscription*")`,
	// Living off the land: certutil download
	`process where process.name : "certutil.exe" and process.args : ("-urlcache", "-decode", "-decodehex")`,
	// BITS
	`process where process.name : "bitsadmin.exe" and process.args : ("/transfer", "/download", "/create")`,
}

func TestRealWorldQueriesParse(t *testing.T) {
	clean, withErrors, noConds := 0, 0, 0
	for _, q := range realWorldQueries {
		r := ExtractConditions(q)
		if r == nil || r.Conditions == nil {
			t.Fatalf("nil result for %q", q)
		}
		for i, c := range r.Conditions {
			if c.Field == "" || c.Operator == "" {
				t.Errorf("query %q condition %d malformed: %+v", q, i, c)
			}
		}
		switch {
		case len(r.Errors) > 0:
			withErrors++
			t.Logf("errors on %q: %v", q, r.Errors)
		case len(r.Conditions) == 0:
			noConds++
		default:
			clean++
		}
	}
	total := len(realWorldQueries)
	t.Logf("real-world: clean=%d, no_conditions=%d, with_errors=%d (total=%d)", clean, noConds, withErrors, total)
	// The whole bank is hand-verified valid EQL; every one must parse cleanly.
	if withErrors > 0 {
		t.Errorf("%d/%d real-world queries produced errors", withErrors, total)
	}
}

func TestRealWorldRoundTrips(t *testing.T) {
	// Every real-world query must survive the parse→render→parse fixpoint.
	all := append([]string{}, realWorldQueries...)
	for _, tc := range realWorldCases {
		all = append(all, tc.query)
	}
	for _, q := range all {
		q1, err := Parse(NormalizeQuery(q))
		if err != nil {
			continue // parse errors covered elsewhere; round-trip only the clean ones
		}
		r1 := q1.String()
		q2, err := Parse(r1)
		if err != nil {
			t.Errorf("re-parse failed for %q → %q: %v", q, r1, err)
			continue
		}
		if r1 != q2.String() {
			t.Errorf("round-trip unstable:\n  from: %s\n  r1:   %s\n  r2:   %s", q, r1, q2.String())
		}
	}
}

func TestRealWorldFieldsAreDeduped(t *testing.T) {
	r := ExtractConditions(`process where process.name : "a" and process.name : "b" and process.name : "c"`)
	count := 0
	for _, f := range r.Fields {
		if f == "process.name" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("process.name appears %d times in Fields, want 1", count)
	}
	_ = strings.TrimSpace
}
