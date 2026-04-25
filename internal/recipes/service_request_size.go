package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// serviceRequestSizeRecipe emits two quantile panels (p50/p95) per matching
// request-size histogram, scoped to the service profile.
type serviceRequestSizeRecipe struct{}

// NewServiceRequestSize returns the service_request_size recipe.
func NewServiceRequestSize() Recipe { return &serviceRequestSizeRecipe{} }

func (serviceRequestSizeRecipe) Name() string    { return "service_request_size" }
func (serviceRequestSizeRecipe) Section() string { return "saturation" }

// Match requires a histogram whose base name ends in _request_size_bytes (with
// or without the _bucket suffix) AND carries at least one HTTP-shape label
// (method or handler). The label guard prevents false positives on
// grpc_server_msg_received_bytes or db_query_bytes_bucket, which are
// histograms but have neither label.
func (r serviceRequestSizeRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeHistogram {
		return false
	}
	name := m.Descriptor.Name
	// Accept both the base name and the _bucket scrape-form.
	name = strings.TrimSuffix(name, "_bucket")
	if !strings.HasSuffix(name, "_request_size_bytes") {
		return false
	}
	// Require at least one HTTP-shape label to distinguish HTTP request size
	// histograms from generic byte-size histograms.
	return m.HasLabel("method") || m.HasLabel("handler")
}

func (r serviceRequestSizeRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
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
			Title:      fmt.Sprintf("Request size (p50/p95): %s", m.Descriptor.Name),
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "bytes",
			Queries:    queries,
			Confidence: 0.75,
			Rationale: fmt.Sprintf(
				"Request-size histogram %q; p50/p95 via histogram_quantile over %s with le grouping.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
