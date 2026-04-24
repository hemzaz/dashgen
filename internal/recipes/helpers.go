package recipes

import (
	"sort"
	"strings"
)

// bannedLabels are high-cardinality identifiers that must never appear in a
// sum-by grouping. Duplicated here (and in internal/safety) intentionally:
// recipes should never build queries that include them even if the safety
// layer would catch it later. Defense in depth.
var bannedLabels = map[string]bool{
	"request_id": true,
	"trace_id":   true,
	"session_id": true,
	"user_id":    true,
}

// safeGroupLabels returns a stable grouping set drawn from the descriptor's
// labels. The result:
//   - always contains "job" (if present on the descriptor)
//   - plus "instance" if present
//   - plus any of the caller's preferred labels (e.g. "route", "handler")
//     that actually exist on the descriptor
//   - never contains a banned label
//   - is sorted alphabetically for determinism
//
// If the descriptor carries none of these, the fallback is ["job"] so the
// caller always emits a valid sum-by grouping.
func safeGroupLabels(m ClassifiedMetricView, preferred ...string) []string {
	have := map[string]bool{}
	for _, l := range m.Descriptor.Labels {
		if bannedLabels[l] {
			continue
		}
		have[l] = true
	}
	chosen := map[string]bool{}
	if have["job"] {
		chosen["job"] = true
	}
	if have["instance"] {
		chosen["instance"] = true
	}
	for _, p := range preferred {
		if bannedLabels[p] {
			continue
		}
		if have[p] {
			chosen[p] = true
		}
	}
	if len(chosen) == 0 {
		return []string{"job"}
	}
	out := make([]string, 0, len(chosen))
	for k := range chosen {
		out = append(out, k)
	}
	// Sort for determinism — map iteration is unstable.
	sort.Strings(out)
	return out
}

// legendFor returns a Grafana legend template referencing all labels in the
// grouping. "{{job}} {{instance}}" etc. When no labels, empty.
func legendFor(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		parts = append(parts, "{{"+l+"}}")
	}
	return strings.Join(parts, " ")
}
