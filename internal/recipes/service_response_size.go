package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// serviceResponseSizeRecipe emits a timeseries panel per matching response-size
// histogram with two query candidates for p50/p95.
type serviceResponseSizeRecipe struct{}

// NewServiceResponseSize returns the service_response_size recipe.
func NewServiceResponseSize() Recipe { return &serviceResponseSizeRecipe{} }

func (serviceResponseSizeRecipe) Name() string    { return "service_response_size" }
func (serviceResponseSizeRecipe) Section() string { return "saturation" }

// Match requires a histogram whose base name ends in _response_size_bytes AND
// carries an HTTP-shape label (method or handler). The name guard ensures we
// never claim _request_size_bytes (handled by the sibling recipe).
func (r serviceResponseSizeRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeHistogram {
		return false
	}
	base := strings.TrimSuffix(m.Descriptor.Name, "_bucket")
	if !strings.HasSuffix(base, "_response_size_bytes") {
		return false
	}
	return m.HasLabel("method") || m.HasLabel("handler")
}

func (r serviceResponseSizeRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	percentiles := []struct {
		quantile float64
		legend   string
	}{
		{0.50, "p50"},
		{0.95, "p95"},
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		// histogram_quantile requires "le" in the grouping. Safe-group adds
		// job/instance/method/handler where present.
		group := safeGroupLabels(m, "method", "handler")
		group = ensureLabel(group, "le")
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
				Unit:         "bytes",
			})
		}
		panels = append(panels, ir.Panel{
			Title:      fmt.Sprintf("Response Size (p50/p95): %s", m.Descriptor.Name),
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "bytes",
			Queries:    queries,
			Confidence: 0.75,
			Rationale: fmt.Sprintf(
				"Response size histogram %q; p50/p95 via histogram_quantile over %s with le grouping.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
