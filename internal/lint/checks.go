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
type Panel struct {
	ID      int64    `json:"id"`
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Targets []Target `json:"targets"`
}

// IsRow reports whether this panel is a Grafana row separator rather
// than a renderable panel. Most checks should skip rows.
func (p Panel) IsRow() bool { return p.Type == "row" }

// Input is the parsed dashboard bundle handed to every check.
type Input struct {
	// Panels is the full panel list from dashboard.json in source order.
	Panels []Panel
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

func init() {
	Register(checkBannedLabel{})
	Register(checkEmptyPanel{})
}
