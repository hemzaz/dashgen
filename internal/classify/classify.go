// Package classify runs deterministic classification over a MetricInventory.
//
// Classification is purely a function of the input inventory. No network,
// no clocks, no randomness. The output is a ClassifiedInventory that wraps
// the (already-sorted) inventory together with per-metric annotations.
//
// Rules are evaluated in a fixed order; the first matching rule wins. The
// rule set is intentionally conservative: if nothing matches, the metric is
// classified as unknown and downstream recipes are expected to skip it
// rather than fabricate a panel.
//
// Rule order (first match wins):
//  1. Explicit Prometheus metadata type (counter/gauge/histogram/summary)
//     when present on the descriptor.
//  2. Suffix-based fallback:
//     - "_bucket", "_sum", "_count" co-existing on the same base name
//     classify the "_bucket" descriptor as histogram.
//     - "_total" -> counter
//     - "_seconds" -> gauge with unit "s"
//     - "_bytes"   -> gauge with unit "bytes"
//     - "_ratio"   -> gauge with unit "percentunit"
//  3. Anything else stays MetricTypeUnknown.
//
// Family inference: the prefix up to the first "_" is the family (e.g.
// "http_requests_total" -> "http"). Empty names get family "".
//
// Traits:
//   - TraitServiceHTTP is attached when labels include any of
//     {method, status_code, route, path}.
//   - TraitLatencyHistogram is attached to a "_bucket" descriptor whose base
//     name contains "duration" or "latency" and whose labels include "le".
package classify

import (
	"strings"

	"dashgen/internal/inventory"
)

// Trait is a classification hint downstream recipes can match on.
type Trait string

const (
	// TraitServiceHTTP marks metrics that look like HTTP service telemetry
	// based on labels such as method/status_code/route/path.
	TraitServiceHTTP Trait = "service_http"

	// TraitLatencyHistogram marks histogram _bucket descriptors whose base
	// name strongly suggests latency/duration semantics.
	TraitLatencyHistogram Trait = "latency_histogram"
)

// ClassifiedMetric is a MetricDescriptor plus the annotations classify added.
//
// The underlying descriptor is carried by value so mutations in later stages
// do not silently mutate the caller's inventory.
type ClassifiedMetric struct {
	Descriptor inventory.MetricDescriptor
	Type       inventory.MetricType
	Family     string
	Unit       string
	Traits     []Trait
}

// HasTrait reports whether the metric carries the given trait.
func (c ClassifiedMetric) HasTrait(t Trait) bool {
	for _, existing := range c.Traits {
		if existing == t {
			return true
		}
	}
	return false
}

// ClassifiedInventory is the inventory plus per-metric classification.
//
// Metrics retains the same stable sort order (by Name) as the input inventory.
type ClassifiedInventory struct {
	Inventory *inventory.MetricInventory
	Metrics   []ClassifiedMetric
}

// Classify applies the deterministic rule set over inv and returns a
// ClassifiedInventory. The input is not mutated.
func Classify(inv *inventory.MetricInventory) *ClassifiedInventory {
	if inv == nil {
		return &ClassifiedInventory{Inventory: &inventory.MetricInventory{}}
	}

	// Build the set of base names that have the full histogram trio
	// (_bucket + _sum + _count) so we can classify _bucket as histogram
	// only when the family is complete. This avoids guessing.
	hasSuffix := map[string]map[string]bool{}
	for _, m := range inv.Metrics {
		base, suffix := splitSuffix(m.Name)
		if suffix == "" {
			continue
		}
		if _, ok := hasSuffix[base]; !ok {
			hasSuffix[base] = map[string]bool{}
		}
		hasSuffix[base][suffix] = true
	}
	isHistogramBucket := func(name string) bool {
		base, suffix := splitSuffix(name)
		if suffix != "bucket" {
			return false
		}
		s := hasSuffix[base]
		return s["sum"] && s["count"]
	}

	out := &ClassifiedInventory{
		Inventory: inv,
		Metrics:   make([]ClassifiedMetric, 0, len(inv.Metrics)),
	}
	for _, m := range inv.Metrics {
		cm := classifyOne(m, isHistogramBucket)
		out.Metrics = append(out.Metrics, cm)
	}
	return out
}

// classifyOne applies the first-match-wins rule set to a single descriptor.
func classifyOne(m inventory.MetricDescriptor, isHistogramBucket func(string) bool) ClassifiedMetric {
	cm := ClassifiedMetric{
		Descriptor: m,
		Type:       inventory.MetricTypeUnknown,
		Family:     familyOf(m.Name),
	}

	// Rule 1: explicit Prometheus metadata wins when present.
	switch m.Type {
	case inventory.MetricTypeCounter,
		inventory.MetricTypeGauge,
		inventory.MetricTypeHistogram,
		inventory.MetricTypeSummary:
		cm.Type = m.Type
	}

	// Rule 2: suffix-based fallback when type is still unknown.
	if cm.Type == inventory.MetricTypeUnknown {
		switch {
		case isHistogramBucket(m.Name):
			cm.Type = inventory.MetricTypeHistogram
		case strings.HasSuffix(m.Name, "_total"):
			cm.Type = inventory.MetricTypeCounter
		case strings.HasSuffix(m.Name, "_seconds"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "s"
		case strings.HasSuffix(m.Name, "_bytes"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "bytes"
		case strings.HasSuffix(m.Name, "_ratio"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "percentunit"
		}
	}

	// Also apply unit hints when the descriptor was typed by metadata but
	// carries a unit-bearing suffix.
	if cm.Unit == "" {
		switch {
		case strings.HasSuffix(m.Name, "_seconds"):
			cm.Unit = "s"
		case strings.HasSuffix(m.Name, "_bytes"):
			cm.Unit = "bytes"
		case strings.HasSuffix(m.Name, "_ratio"):
			cm.Unit = "percentunit"
		}
	}

	// Trait: service HTTP hint. The label set covers both the
	// "{method,status_code,route,path}" convention and Go promhttp's
	// "{handler,code}" convention.
	if hasAnyLabel(m.Labels, "method", "status_code", "route", "path", "handler", "code") {
		cm.Traits = append(cm.Traits, TraitServiceHTTP)
	}

	// Trait: latency histogram. Two paths:
	//   1. Name ends in "_bucket" with the full _sum+_count trio AND has `le`.
	//   2. Type was set to histogram by metadata (no _bucket suffix on name)
	//      and the name itself contains duration/latency. The `le` label is
	//      assumed because histogram metadata implies a bucket series.
	nameLower := strings.ToLower(m.Name)
	switch {
	case isHistogramBucket(m.Name):
		base, _ := splitSuffix(m.Name)
		bl := strings.ToLower(base)
		if (strings.Contains(bl, "duration") || strings.Contains(bl, "latency")) && hasLabel(m.Labels, "le") {
			cm.Traits = append(cm.Traits, TraitLatencyHistogram)
		}
	case cm.Type == inventory.MetricTypeHistogram:
		// Exclude _sum/_count/_bucket partials. The bare base name
		// (returned by Prometheus's metadata API) is what we want to
		// trait; recipes append _bucket when synthesizing the query.
		if !strings.HasSuffix(m.Name, "_sum") &&
			!strings.HasSuffix(m.Name, "_count") &&
			!strings.HasSuffix(m.Name, "_bucket") &&
			(strings.Contains(nameLower, "duration") || strings.Contains(nameLower, "latency")) {
			cm.Traits = append(cm.Traits, TraitLatencyHistogram)
		}
	}

	return cm
}

// familyOf returns the prefix up to (but not including) the first "_". For
// a name with no underscore, the whole name is the family.
func familyOf(name string) string {
	if name == "" {
		return ""
	}
	i := strings.Index(name, "_")
	if i < 0 {
		return name
	}
	return name[:i]
}

// splitSuffix splits a metric name into (base, suffix) on the last "_"
// when the trailing token is one of the recognized histogram/counter
// suffixes. Anything else returns ("", "").
func splitSuffix(name string) (string, string) {
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return "", ""
	}
	suffix := name[i+1:]
	switch suffix {
	case "bucket", "sum", "count", "total":
		return name[:i], suffix
	}
	return "", ""
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func hasAnyLabel(labels []string, wanted ...string) bool {
	for _, w := range wanted {
		if hasLabel(labels, w) {
			return true
		}
	}
	return false
}
