package smoke

import (
	"fmt"
	"regexp"
	"strings"
)

// redactPlaceholder is the fixed sentinel internal/http RedactURL/RedactError leave in
// place of a scrubbed value; scrubSecretValues treats it as already-safe.
const redactPlaceholder = "REDACTED"

// Check names and Finding statuses used across the suite.
const (
	CheckParity   = "parity"
	CheckAppSync  = "app-sync"
	CheckCache    = "cache"
	CheckFLBypass = "fl-bypass"

	StatusPass = "PASS"
	StatusFail = "FAIL"
	StatusSkip = "SKIP"
	// StatusNA marks a check that is not comparable (e.g. an indexer harbrr serves but
	// Prowlarr does not have) — informational, never a failure.
	StatusNA = "N/A"
)

// Finding is one check's outcome for one indexer. Detail is already secret-scrubbed at
// its source (every URL through RedactURL, every error through RedactError); the report
// renderer applies one final defensive scrub before writing.
type Finding struct {
	Indexer string
	Check   string
	Status  string
	Detail  string
}

// Report is the full smoke run: the query used and every check's Finding.
type Report struct {
	Query    string
	Findings []Finding
}

// HasFailures reports whether any check failed (the CLI exits non-zero when true).
func (r Report) HasFailures() bool {
	for _, f := range r.Findings {
		if f.Status == StatusFail {
			return true
		}
	}
	return false
}

// counts tallies findings by status for the summary line.
func (r Report) counts() (pass, fail, skip, na int) {
	for _, f := range r.Findings {
		switch f.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusSkip:
			skip++
		case StatusNA:
			na++
		}
	}
	return pass, fail, skip, na
}

// Summary is a one-line terminal summary of the run.
func (r Report) Summary() string {
	pass, fail, skip, na := r.counts()
	verdict := "PASS"
	if fail > 0 {
		verdict = "FAIL"
	}
	return fmt.Sprintf("smoke %s: %d passed, %d failed, %d skipped, %d not-comparable", verdict, pass, fail, skip, na)
}

// Markdown renders the failures-first report. Every dynamic string is already redacted
// at its source; as defense in depth the assembled document is gated against the same
// credential token list ValidateNoSecrets uses and any leaked token=value is scrubbed
// before the text is returned, so a secret can never reach the report file.
func (r Report) Markdown() string {
	text := r.render()
	if err := ValidateNoSecrets(EvidenceRecord{Tracker: "report", Notes: text}); err != nil {
		text = scrubSecretValues(text)
	}
	return text
}

// render assembles the markdown body (pre-scrub).
func (r Report) render() string {
	pass, fail, skip, na := r.counts()
	var b strings.Builder
	b.WriteString("# harbrr smoke report\n\n")
	fmt.Fprintf(&b, "- query: `%s`\n", r.Query)
	fmt.Fprintf(&b, "- result: **%s**\n", map[bool]string{true: "FAIL", false: "PASS"}[fail > 0])
	fmt.Fprintf(&b, "- checks: %d passed, %d failed, %d skipped, %d not-comparable\n\n", pass, fail, skip, na)

	r.writeSection(&b, "Failures", StatusFail)
	r.writeSection(&b, "Not comparable", StatusNA)
	r.writeFullTable(&b)
	return b.String()
}

// writeSection writes a bulleted section of every finding with the given status (only
// when at least one exists).
func (r Report) writeSection(b *strings.Builder, title, status string) {
	first := true
	for _, f := range r.Findings {
		if f.Status != status {
			continue
		}
		if first {
			fmt.Fprintf(b, "## %s\n\n", title)
			first = false
		}
		fmt.Fprintf(b, "- **%s** / %s — %s\n", f.Indexer, f.Check, f.Detail)
	}
	if !first {
		b.WriteString("\n")
	}
}

// writeFullTable writes the collapsible table of every finding.
func (r Report) writeFullTable(b *strings.Builder) {
	b.WriteString("<details>\n<summary>All checks</summary>\n\n")
	b.WriteString("| Indexer | Check | Status | Detail |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, f := range r.Findings {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", f.Indexer, f.Check, f.Status, mdCell(f.Detail))
	}
	b.WriteString("\n</details>\n")
}

// mdCell makes a detail string safe to place in a table cell (escape the pipe, flatten
// newlines).
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// secretValueRe matches a credential token immediately followed by an assignment and a
// value, so a genuinely leaked secret (token=VALUE) can be scrubbed from the final
// document. A value already reduced to the RedactURL/RedactError placeholder is left
// untouched by scrubSecretValues.
var secretValueRe = regexp.MustCompile(`(?i)(` + strings.Join(secretTokens, "|") + `)(\s*[=:]\s*"?)([^"\s&<>]+)`)

// scrubSecretValues replaces any token=value whose value is not already the redaction
// placeholder, closing the gap the source-site redactors are expected to have already
// covered.
func scrubSecretValues(s string) string {
	return secretValueRe.ReplaceAllStringFunc(s, func(m string) string {
		g := secretValueRe.FindStringSubmatch(m)
		if len(g) == 4 && strings.EqualFold(g[3], redactPlaceholder) {
			return m
		}
		return g[1] + g[2] + redactPlaceholder
	})
}
