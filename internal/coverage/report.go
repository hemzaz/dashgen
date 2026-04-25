// Package coverage computes dashboard coverage reports: given a
// metric inventory and (optionally) the set of metrics referenced by
// a dashboard, it summarises what's covered, what's not, and how
// uncovered metrics cluster by name family.
//
// Determinism: outputs are sorted by deterministic keys end-to-end so
// two runs over identical inputs produce byte-identical JSON.
//
// AI-free: family grouping is naive string-prefix-up-to-first-
// underscore grouping. The AI-driven clustering lands in Phase 5
// behind an explicit opt-in (see V0.2-PLAN §"unknown-grouping"); this
// package's output is the deterministic baseline.
package coverage

import (
	"sort"
	"strings"
)

// Report is the JSON document `dashgen coverage` emits. The shape is
// the public CLI contract; renaming any json tag is a breaking change.
type Report struct {
	SourceInventory string   `json:"source_inventory"`
	SourceDashboard string   `json:"source_dashboard,omitempty"`
	Summary         Summary  `json:"summary"`
	Covered         []string `json:"covered"`
	Uncovered       []string `json:"uncovered"`
	UnknownFamilies []Family `json:"unknown_families"`
}

// Summary is the headline numeric report: how many metrics in the
// inventory, how many are covered by panels, how many are not.
type Summary struct {
	MetricsTotal     int `json:"metrics_total"`
	MetricsCovered   int `json:"metrics_covered"`
	MetricsUncovered int `json:"metrics_uncovered"`
}

// Family groups uncovered metrics by their leading-underscore prefix.
// Members within a family are sorted lexically.
type Family struct {
	Name    string   `json:"family"`
	Count   int      `json:"count"`
	Metrics []string `json:"metrics"`
}

// Compute computes coverage of the given inventory against the metrics
// referenced by a dashboard (dashboardRefs). Both arguments are
// treated as multisets — duplicates are de-duplicated. The order of
// the arguments is irrelevant.
//
// Returned slices are sorted: Covered + Uncovered lexically by metric
// name; UnknownFamilies primarily by descending Count, then by family
// name lexically (so the most-uncovered family surfaces first).
func Compute(inventory []string, dashboardRefs []string) (Summary, []string, []string, []Family) {
	invSet := make(map[string]struct{}, len(inventory))
	for _, m := range inventory {
		invSet[m] = struct{}{}
	}
	refSet := make(map[string]struct{}, len(dashboardRefs))
	for _, m := range dashboardRefs {
		if _, ok := invSet[m]; ok {
			refSet[m] = struct{}{}
		}
	}

	sortedInventory := append([]string(nil), inventory...)
	sort.Strings(sortedInventory)
	covered := make([]string, 0, len(refSet))
	uncovered := make([]string, 0)
	seen := map[string]struct{}{}
	for _, m := range sortedInventory {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if _, isRef := refSet[m]; isRef {
			covered = append(covered, m)
		} else {
			uncovered = append(uncovered, m)
		}
	}

	byFamily := map[string][]string{}
	for _, m := range uncovered {
		f := FamilyOf(m)
		byFamily[f] = append(byFamily[f], m)
	}
	families := make([]Family, 0, len(byFamily))
	for name, metrics := range byFamily {
		sort.Strings(metrics)
		families = append(families, Family{Name: name, Count: len(metrics), Metrics: metrics})
	}
	sort.SliceStable(families, func(i, j int) bool {
		if families[i].Count != families[j].Count {
			return families[i].Count > families[j].Count
		}
		return families[i].Name < families[j].Name
	})

	summary := Summary{
		MetricsTotal:     len(seen),
		MetricsCovered:   len(covered),
		MetricsUncovered: len(uncovered),
	}
	return summary, covered, uncovered, families
}

// FamilyOf returns the prefix of name up to (but not including) the
// first underscore. A name with no underscore is its own family.
//
// Examples:
//   - http_requests_total → "http"
//   - node_cpu_seconds_total → "node"
//   - up → "up"
//   - "" → ""
func FamilyOf(name string) string {
	if i := strings.Index(name, "_"); i >= 0 {
		return name[:i]
	}
	return name
}

// ExtractReferencedMetrics returns every metric name from inventory
// that appears in any of the given expression strings. Used by the
// orchestrator to convert the on-disk dashboard PromQL into a
// reference list for Compute. The match is identifier-boundary aware
// so a metric `foo` is not falsely matched against a label `foo_bar`.
//
// Output is the unique referenced subset, sorted for determinism.
func ExtractReferencedMetrics(inventory []string, exprs []string) []string {
	if len(inventory) == 0 || len(exprs) == 0 {
		return nil
	}
	invSet := make(map[string]struct{}, len(inventory))
	for _, m := range inventory {
		invSet[m] = struct{}{}
	}
	hits := map[string]struct{}{}
	for _, e := range exprs {
		i := 0
		for i < len(e) {
			if !isIdentStart(e[i]) {
				i++
				continue
			}
			start := i
			for i < len(e) && isIdentCont(e[i]) {
				i++
			}
			tok := e[start:i]
			if _, ok := invSet[tok]; ok {
				hits[tok] = struct{}{}
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, 0, len(hits))
	for m := range hits {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

func isIdentStart(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c == '_':
		return true
	}
	return false
}

func isIdentCont(c byte) bool {
	if isIdentStart(c) {
		return true
	}
	return c >= '0' && c <= '9'
}
