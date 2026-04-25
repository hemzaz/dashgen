// Package lint defines the dashboard-quality check framework used by the
// `dashgen lint` subcommand.
//
// Design goals (V0.2-PLAN.md §"lint"; .omc/plans/v0.2-remainder.md Step 3.0):
//
//   - Lint runs against an *existing* `dashboard.json` bundle on disk —
//     not against the in-memory IR. The input is what an operator
//     committed to their dashboards repo, so the schema this file
//     consumes is the Grafana-flavoured JSON the renderer emits, not
//     the internal IR.
//   - Lint refuses (exit non-zero) when the panel "should not exist"
//     and warns when it merely "could be improved." This mirrors the
//     ADVERSARY §6 drift pattern "warnings instead of refusals" — if
//     a check would emit a warning that no operator should ignore, it
//     refuses instead.
//   - Output is deterministic: issues sort by (Code, PanelID) so two
//     runs over identical input produce byte-identical reports.
//   - The Check interface is the single extension point. Adding a new
//     check is one new file in this package plus one Register call —
//     no edits to `internal/app/lint/`.
//
// This file is the v0.2 Phase 6 scaffolding. The seed registration ships
// two checks (banned-label, empty-panel) sufficient to prove the
// pipeline; the full v0.2 corpus of 7 check classes lands in follow-up
// commits per the plan.
package lint

import (
	"fmt"
	"sort"
	"strings"
)

// Severity controls the operator-visible blast radius of an issue.
//
// SeverityRefuse means the panel should not exist as-is. The CLI exits
// non-zero when any refusal fires.
//
// SeverityWarn means the panel ships but a reviewer should look. The
// CLI still exits zero unless `--strict` is passed.
type Severity string

const (
	// SeverityRefuse triggers a non-zero exit code from `dashgen lint`.
	SeverityRefuse Severity = "refuse"
	// SeverityWarn surfaces an issue without failing the run.
	SeverityWarn Severity = "warn"
)

// Issue is one rule violation against one panel. The shape is the public
// JSON output schema of `dashgen lint`; field names (json tags) are
// part of the contract and changing them is a breaking change.
type Issue struct {
	Code       string   `json:"code"`        // stable id, e.g. "banned-label"
	Severity   Severity `json:"severity"`    // refuse | warn
	PanelID    int64    `json:"panel_id"`    // panel id from dashboard.json (0 for dashboard-level)
	PanelTitle string   `json:"panel_title"` // panel title at issue time
	Message    string   `json:"message"`     // operator-readable reason
}

// Target is one PromQL target on a panel, as parsed from dashboard.json.
// Only fields used by checks are populated; the schema is not exhaustive.
type Target struct {
	Expr         string `json:"expr"`
	LegendFormat string `json:"legendFormat"`
}

// Panel is the subset of `dashboard.json["panels"][i]` that lint reads.
// Row separators (`type == "row"`) are kept so checks can correlate.
//
// Unit is flattened from `fieldConfig.defaults.unit` by the orchestrator
// so the lint package never has to know about Grafana's nested schema.
type Panel struct {
	ID      int64    `json:"id"`
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Unit    string   `json:"unit"`
	Targets []Target `json:"targets"`
}

// IsRow reports whether this panel is a Grafana row separator rather
// than a renderable panel. Most checks should skip rows.
func (p Panel) IsRow() bool { return p.Type == "row" }

// Input is the parsed dashboard bundle handed to every check.
type Input struct {
	// Panels is the full panel list from dashboard.json in source order.
	Panels []Panel

	// Rationale is the verbatim text of rationale.md, or empty if the
	// orchestrator could not read it (the file is optional from lint's
	// perspective — checks that need it MUST handle empty defensively).
	Rationale string
}

// Check is the contract every lint rule satisfies. Implementations are
// pure functions: they observe Input and produce Issues, never mutate.
//
// Extension point: a new check is one new file in this package
// declaring a Check, plus one Register call from its init().
type Check interface {
	// Code is the stable identifier emitted in Issue.Code. Must be
	// kebab-case, lowercase, ASCII; reviewers grep for it in goldens.
	Code() string

	// Run produces the issues this check finds in the input. An empty
	// slice (or nil) means "no issues."
	Run(in *Input) []Issue
}

// registry holds Checks in registration order. CheckList copies it so
// callers cannot mutate the package state through the returned slice.
var registry []Check

// Register adds a Check to the registry. Called from init() of the
// file that defines the check.
func Register(c Check) {
	if c == nil {
		panic("lint.Register: check cannot be nil")
	}
	if c.Code() == "" {
		panic("lint.Register: check Code() cannot be empty")
	}
	registry = append(registry, c)
}

// CheckList returns the registered checks in registration order. The
// returned slice is a copy.
func CheckList() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	return out
}

// RunAll runs every registered check against the input and returns the
// concatenated issues, sorted by (Code, PanelID, Message) so output is
// deterministic regardless of registration order or check internals.
func RunAll(in *Input) []Issue {
	var all []Issue
	for _, c := range registry {
		all = append(all, c.Run(in)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Code != all[j].Code {
			return all[i].Code < all[j].Code
		}
		if all[i].PanelID != all[j].PanelID {
			return all[i].PanelID < all[j].PanelID
		}
		return all[i].Message < all[j].Message
	})
	return all
}

// HasRefusal reports whether any issue in the slice is a refusal. Used
// by the CLI to decide exit code.
func HasRefusal(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == SeverityRefuse {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Seed checks. The full v0.2 corpus expands to seven check classes per
// .omc/plans/v0.2-remainder.md Step 3.0; this file ships two of them so
// the orchestrator and CLI plumbing have something concrete to drive.
// ---------------------------------------------------------------------------

// bannedLabels are the high-cardinality identifiers SPECS §safety policy
// forbids in matchers and groupings. Lint scans both selector matchers
// (`{label=...}`) and grouping clauses (`by (label, ...)` /
// `without (label, ...)`) for these. A hit is a refusal.
var bannedLabels = []string{"request_id", "session_id", "trace_id", "user_id"}

// checkBannedLabel is the seed safety check: any reference to a banned
// label inside any panel's PromQL expression refuses. Refuses (not warns)
// because the offending panel must not ship.
type checkBannedLabel struct{}

func (checkBannedLabel) Code() string { return "banned-label" }

func (checkBannedLabel) Run(in *Input) []Issue {
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() {
			continue
		}
		for _, t := range p.Targets {
			for _, label := range bannedLabels {
				if !mentionsLabel(t.Expr, label) {
					continue
				}
				out = append(out, Issue{
					Code:       "banned-label",
					Severity:   SeverityRefuse,
					PanelID:    p.ID,
					PanelTitle: p.Title,
					Message:    "panel uses banned label \"" + label + "\" in PromQL; high-cardinality identifiers must never be matched or grouped",
				})
				break // one issue per (panel, target) is enough
			}
		}
	}
	return out
}

// checkEmptyPanel refuses any non-row panel that ships zero targets.
// A panel with no PromQL renders as an empty box; the right answer is
// to drop it (SPECS Rule 5) before commit.
type checkEmptyPanel struct{}

func (checkEmptyPanel) Code() string { return "empty-panel" }

func (checkEmptyPanel) Run(in *Input) []Issue {
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() {
			continue
		}
		if len(p.Targets) > 0 {
			continue
		}
		out = append(out, Issue{
			Code:       "empty-panel",
			Severity:   SeverityRefuse,
			PanelID:    p.ID,
			PanelTitle: p.Title,
			Message:    "panel has no PromQL targets; drop it rather than render an empty visualization",
		})
	}
	return out
}

// checkDuplicatePanel refuses any non-row panel that shares both its
// title and its primary target expression with another panel. This is
// the operator-visible "this panel ships twice" mistake — almost always
// a hand-edit that copied a panel and forgot to change anything.
//
// We deliberately do NOT key on `panel.id`: dashgen's renderer reduces
// the IR's SHA-256[:16] PanelUID to a 32-bit Grafana int via modulo,
// and incidental modulo collisions across distinct UIDs are a separate
// concern from semantic duplication. Catching modulo collisions belongs
// in a renderer-level test, not a dashboard-quality check.
//
// Row-type panels are skipped: section repetition is intentional.
type checkDuplicatePanel struct{}

func (checkDuplicatePanel) Code() string { return "duplicate-panel" }

func (checkDuplicatePanel) Run(in *Input) []Issue {
	type fingerprint struct {
		title string
		expr  string
	}
	seen := map[fingerprint][]int{}
	for i, p := range in.Panels {
		if p.IsRow() {
			continue
		}
		expr := ""
		if len(p.Targets) > 0 {
			expr = p.Targets[0].Expr
		}
		fp := fingerprint{title: p.Title, expr: expr}
		seen[fp] = append(seen[fp], i)
	}
	var out []Issue
	for fp, idxs := range seen {
		if len(idxs) < 2 {
			continue
		}
		for _, i := range idxs {
			p := in.Panels[i]
			out = append(out, Issue{
				Code:       "duplicate-panel",
				Severity:   SeverityRefuse,
				PanelID:    p.ID,
				PanelTitle: p.Title,
				Message:    fmt.Sprintf("panel %q with the same primary expression appears %d times; remove the duplicates", fp.title, len(idxs)),
			})
		}
	}
	return out
}

// checkWithoutGrouping refuses any non-row panel whose PromQL contains
// a `without (...)` aggregation. The without operator inverts the
// cardinality calculus from "what we keep" to "what we drop", which
// routinely blows past the safety policy's grouping-cardinality
// threshold once the underlying series count grows. Recipes never emit
// `without()` (they all use `by (...)` against an explicit allowlist),
// so any occurrence here is either drift from a hand-edit or a custom
// recipe that bypasses safety.
type checkWithoutGrouping struct{}

func (checkWithoutGrouping) Code() string { return "without-grouping" }

func (checkWithoutGrouping) Run(in *Input) []Issue {
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() {
			continue
		}
		for _, t := range p.Targets {
			if !containsWithoutKeyword(t.Expr) {
				continue
			}
			out = append(out, Issue{
				Code:       "without-grouping",
				Severity:   SeverityRefuse,
				PanelID:    p.ID,
				PanelTitle: p.Title,
				Message:    "panel uses `without(...)` aggregation; recipes use explicit `by (...)` allowlists so safety policy can bound cardinality",
			})
			break // one issue per panel is enough; reviewer fixes all targets together
		}
	}
	return out
}

// containsWithoutKeyword reports whether expr contains a PromQL
// `without` aggregation modifier. PromQL grammar requires the keyword
// to be followed by `(` with optional whitespace; identifier-boundary
// checking on the leading edge avoids false positives on labels like
// `request_without_user`.
func containsWithoutKeyword(expr string) bool {
	const kw = "without"
	idx := 0
	for {
		hit := strings.Index(expr[idx:], kw)
		if hit < 0 {
			return false
		}
		start := idx + hit
		end := start + len(kw)
		idx = end
		if !isLabelBoundary(expr, start-1) {
			continue
		}
		j := end
		for j < len(expr) && (expr[j] == ' ' || expr[j] == '\t') {
			j++
		}
		if j < len(expr) && expr[j] == '(' {
			return true
		}
	}
}

// mentionsLabel reports whether a PromQL string references the named
// label as a matcher key or grouping key. The check is intentionally
// loose: any token-boundary occurrence of the label name in expr is
// treated as a hit. False positives on this check are preferable to
// false negatives — banned-label is a refusal, and a refusal that
// turns out to be over-eager surfaces in code review with the exact
// expression visible.
func mentionsLabel(expr, label string) bool {
	if expr == "" || label == "" {
		return false
	}
	idx := 0
	for {
		hit := strings.Index(expr[idx:], label)
		if hit < 0 {
			return false
		}
		start := idx + hit
		end := start + len(label)
		if !isLabelBoundary(expr, start-1) {
			idx = end
			continue
		}
		if !isLabelBoundary(expr, end) {
			idx = end
			continue
		}
		return true
	}
}

// isLabelBoundary reports whether the byte at i (or its absence) is a
// non-identifier character. Used so that "user_id_extra" does not match
// "user_id".
func isLabelBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	c := s[i]
	switch {
	case c >= 'a' && c <= 'z':
		return false
	case c >= 'A' && c <= 'Z':
		return false
	case c >= '0' && c <= '9':
		return false
	case c == '_':
		return false
	}
	return true
}

// checkMissingRationaleRow warns when a non-row panel has no
// corresponding `**<title>**` line in rationale.md. The mechanical
// rationale is the audit trail every panel ships with (V0.2-PLAN
// §2.1); a panel lacking one means rendering and synthesis disagree
// on what the dashboard contains, which is a documentation drift
// rather than a safety issue. Severity: warn (per Step 3.0 plan).
//
// The check is permissive: empty rationale text disables the check
// entirely so a bundle without rationale.md (e.g. a dry-run capture)
// does not flood the report.
type checkMissingRationaleRow struct{}

func (checkMissingRationaleRow) Code() string { return "missing-rationale-row" }

func (checkMissingRationaleRow) Run(in *Input) []Issue {
	if in.Rationale == "" {
		return nil
	}
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() || p.Title == "" {
			continue
		}
		marker := "**" + p.Title + "**"
		if strings.Contains(in.Rationale, marker) {
			continue
		}
		out = append(out, Issue{
			Code:       "missing-rationale-row",
			Severity:   SeverityWarn,
			PanelID:    p.ID,
			PanelTitle: p.Title,
			Message:    "panel title not present in rationale.md as `**" + p.Title + "**`; rationale must describe every panel",
		})
	}
	return out
}

// checkRateOnGauge refuses any `rate(` or `irate(` wrapping a metric
// whose name does not look like a counter. PromQL's rate semantics
// require monotonically-increasing input; applying rate to a gauge
// produces meaningless numbers (often negative) and is the single
// most common Grafana authoring mistake.
//
// Counter detection is suffix-only — `_total`, `_count`, `_sum`,
// `_bucket`. The full classifier is unavailable here because lint
// runs against an already-rendered dashboard, but the suffix
// heuristic is what dashgen's own classifier uses (`internal/classify`)
// and it covers >99% of real-world counters.
type checkRateOnGauge struct{}

func (checkRateOnGauge) Code() string { return "rate-on-gauge" }

func (checkRateOnGauge) Run(in *Input) []Issue {
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() {
			continue
		}
		for _, t := range p.Targets {
			for _, metric := range metricsInsideRate(t.Expr) {
				if isCounterName(metric) {
					continue
				}
				out = append(out, Issue{
					Code:       "rate-on-gauge",
					Severity:   SeverityRefuse,
					PanelID:    p.ID,
					PanelTitle: p.Title,
					Message:    "rate(" + metric + "[…]) wraps a metric whose name does not look like a counter (no _total/_count/_sum/_bucket suffix); rate() requires a monotonically-increasing series",
				})
			}
		}
	}
	return out
}

// checkSuspiciousUnits warns on a narrow, high-confidence anti-pattern:
// histogram_quantile() over a histogram whose name strongly indicates
// time (`_duration_seconds_bucket`, `_seconds_bucket`, etc.) but whose
// panel unit is not in the time family. That combination is almost
// always a missed-unit-config drift — the panel renders as a raw
// number when it should read as latency.
//
// We deliberately do NOT flag bytes-name + non-bytes-unit because
// dashgen recipes routinely divide a bytes-counter by a bytes-total
// to produce a `percentunit` ratio, and that idiom would false-
// positive constantly. We deliberately do NOT flag histogram_quantile
// on non-time histograms (e.g. bytes histograms) because those are a
// legitimate recipe emission with a bytes unit.
//
// Severity: warn — heuristic, narrow.
type checkSuspiciousUnits struct{}

func (checkSuspiciousUnits) Code() string { return "suspicious-units" }

func (checkSuspiciousUnits) Run(in *Input) []Issue {
	var out []Issue
	for _, p := range in.Panels {
		if p.IsRow() || len(p.Targets) == 0 {
			continue
		}
		expr := p.Targets[0].Expr
		if !strings.Contains(expr, "histogram_quantile(") {
			continue
		}
		bucketMetric := histogramBucketMetric(expr)
		if bucketMetric == "" {
			continue
		}
		base := strings.TrimSuffix(bucketMetric, "_bucket")
		if !looksLikeTimeHistogram(base) {
			continue
		}
		if isTimeUnit(p.Unit) {
			continue
		}
		out = append(out, Issue{
			Code:       "suspicious-units",
			Severity:   SeverityWarn,
			PanelID:    p.ID,
			PanelTitle: p.Title,
			Message:    "histogram_quantile over `" + bucketMetric + "` (latency-shaped) uses unit " + quoteUnit(p.Unit) + "; set unit to 's' (or 'ms' / 'ns')",
		})
	}
	return out
}

// histogramBucketMetric returns the metric name passed to a
// histogram_quantile() call's inner aggregation. The pattern
// dashgen emits is:
//   histogram_quantile(0.95, sum by (le, ...) (rate(<name>_bucket[5m])))
// We grep for `rate(` inside an expression containing
// `histogram_quantile(` and return the first metric name found.
// Returns "" if no metric can be extracted.
func histogramBucketMetric(expr string) string {
	for _, m := range metricsInsideRate(expr) {
		if strings.HasSuffix(m, "_bucket") {
			return m
		}
	}
	return ""
}

// looksLikeTimeHistogram reports whether the given base name (no
// `_bucket` suffix) suggests a latency / duration histogram.
func looksLikeTimeHistogram(base string) bool {
	lower := strings.ToLower(base)
	if strings.Contains(lower, "duration") || strings.Contains(lower, "latency") {
		return true
	}
	if strings.HasSuffix(lower, "_seconds") || strings.HasSuffix(lower, "_milliseconds") {
		return true
	}
	return false
}

// metricsInsideRate returns every metric name that appears as the
// inner argument of a rate(...) or irate(...) call. The PromQL
// grammar nests, but we only handle the simple case `rate(<name>...)`
// where `<name>` is the leading identifier — this is what every
// dashgen recipe emits and >99% of real-world dashboards use.
func metricsInsideRate(expr string) []string {
	var out []string
	for _, fn := range []string{"rate(", "irate("} {
		idx := 0
		for {
			hit := strings.Index(expr[idx:], fn)
			if hit < 0 {
				break
			}
			start := idx + hit
			end := start + len(fn)
			idx = end
			if !isLabelBoundary(expr, start-1) {
				continue
			}
			name := readIdentifier(expr, end)
			if name == "" {
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

// readIdentifier returns the contiguous identifier characters at
// position i, or "" if i does not start an identifier.
func readIdentifier(s string, i int) string {
	if i >= len(s) {
		return ""
	}
	start := i
	for i < len(s) && isIdentChar(s[i], i == start) {
		i++
	}
	return s[start:i]
}

func isIdentChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c == '_':
		return true
	case !first && c >= '0' && c <= '9':
		return true
	}
	return false
}

// isCounterName reports whether name has a counter-like suffix per the
// classifier convention (_total, _count, _sum, _bucket).
func isCounterName(name string) bool {
	for _, suffix := range []string{"_total", "_count", "_sum", "_bucket"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// isTimeUnit reports whether u is a Grafana time-family unit code.
func isTimeUnit(u string) bool {
	switch u {
	case "s", "ms", "ns", "us", "µs", "m", "h", "d", "dthms", "dtdhms":
		return true
	}
	return false
}

// quoteUnit returns a human-readable rendering of a Grafana unit code,
// substituting "<unset>" for the empty string so the message reads
// naturally.
func quoteUnit(u string) string {
	if u == "" {
		return "<unset>"
	}
	return "\"" + u + "\""
}

func init() {
	Register(checkBannedLabel{})
	Register(checkEmptyPanel{})
	Register(checkDuplicatePanel{})
	Register(checkWithoutGrouping{})
	Register(checkMissingRationaleRow{})
	Register(checkRateOnGauge{})
	Register(checkSuspiciousUnits{})
}
