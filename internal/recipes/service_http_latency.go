package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// serviceHTTPLatencyRecipe emits a single timeseries panel per matching
// histogram that carries three query candidates for p50/p95/p99.
type serviceHTTPLatencyRecipe struct{}

// NewServiceHTTPLatency returns the service_http_latency recipe.
func NewServiceHTTPLatency() Recipe { return &serviceHTTPLatencyRecipe{} }

func (serviceHTTPLatencyRecipe) Name() string    { return "service_http_latency" }
func (serviceHTTPLatencyRecipe) Section() string { return "latency" }

// Match requires a histogram carrying the latency_histogram trait. The trait
// itself already required the "_bucket" suffix + "le" label + a base name
// containing "duration" or "latency", so this stays conservative.
func (r serviceHTTPLatencyRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeHistogram && m.HasTrait("latency_histogram")
}

func (r serviceHTTPLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	// Percentiles are deliberately ordered low-to-high so legend order is
	// predictable in the rendered panel.
	percentiles := []struct {
		quantile float64
		legend   string
	}{
		{0.50, "p50"},
		{0.95, "p95"},
		{0.99, "p99"},
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		// histogram_quantile requires "le" in the grouping. Safe-group adds
		// job/instance/route where present.
		group := safeGroupLabels(m, "route", "handler")
		// "le" must be present for histogram_quantile; inject it and keep
		// the sort stable.
		group = ensureLabel(group, "le")
		// Prometheus's metadata API returns histogram base names (without
		// _bucket); the queryable series is always _bucket.
		queryName := m.Descriptor.Name
		if !strings.HasSuffix(queryName, "_bucket") {
			queryName += "_bucket"
		}
		queries := make([]ir.QueryCandidate, 0, len(percentiles))
		for _, pct := range percentiles {
			expr := fmt.Sprintf(
				"histogram_quantile(%.2f, sum by (%s) (rate(%s[%s])))",
				pct.quantile, strings.Join(group, ", "), queryName, defaultRateWindow,
			)
			queries = append(queries, ir.QueryCandidate{
				Expr:         expr,
				LegendFormat: pct.legend + " " + legendFor(without(group, "le")),
				Unit:         "s",
			})
		}
		panels = append(panels, ir.Panel{
			Title:      fmt.Sprintf("Latency (p50/p95/p99): %s", m.Descriptor.Name),
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.75,
			Rationale: fmt.Sprintf(
				"Latency histogram %q; p50/p95/p99 via histogram_quantile over %s with le grouping.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

// ensureLabel returns a sorted copy of labels with want included.
func ensureLabel(labels []string, want string) []string {
	for _, l := range labels {
		if l == want {
			return labels
		}
	}
	out := append([]string(nil), labels...)
	out = append(out, want)
	sort.Strings(out)
	return out
}

// without returns a copy of labels with drop removed, preserving order.
func without(labels []string, drop string) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == drop {
			continue
		}
		out = append(out, l)
	}
	return out
}
