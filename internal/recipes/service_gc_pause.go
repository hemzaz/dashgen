package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const gcPauseMetricName = "go_gc_duration_seconds"

// serviceGCPauseRecipe emits a single timeseries panel showing GC pause
// duration. It handles two exposition shapes:
//   - Summary (Go pre-1.24): pre-computed quantiles via the quantile label.
//   - Histogram (newer Go): p50/p95/p99 via histogram_quantile.
type serviceGCPauseRecipe struct{}

// NewServiceGCPause returns the service_gc_pause recipe.
func NewServiceGCPause() Recipe { return &serviceGCPauseRecipe{} }

func (serviceGCPauseRecipe) Name() string    { return "service_gc_pause" }
func (serviceGCPauseRecipe) Section() string { return "saturation" }

// Match fires on the exact metric name go_gc_duration_seconds regardless of
// whether the runtime exposes it as a summary or histogram.
func (r serviceGCPauseRecipe) Match(m ClassifiedMetricView) bool {
	return m.Descriptor.Name == gcPauseMetricName &&
		(m.Type == inventory.MetricTypeSummary || m.Type == inventory.MetricTypeHistogram)
}

func (r serviceGCPauseRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		var panel ir.Panel
		if m.Type == inventory.MetricTypeSummary {
			panel = r.buildSummaryPanel(m)
		} else {
			panel = r.buildHistogramPanel(m)
		}
		panels = append(panels, panel)
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

// buildSummaryPanel emits a single query that selects the p99 quantile from
// the pre-computed summary labels. Summaries expose quantiles directly via
// the quantile label, so no histogram_quantile is needed.
func (serviceGCPauseRecipe) buildSummaryPanel(m ClassifiedMetricView) ir.Panel {
	group := safeGroupLabels(m)
	expr := fmt.Sprintf(
		`avg by (%s) (%s{quantile="0.99"})`,
		strings.Join(group, ", "), gcPauseMetricName,
	)
	return ir.Panel{
		Title: fmt.Sprintf("GC pause: %s", gcPauseMetricName),
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "s",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "p99 " + legendFor(group),
			Unit:         "s",
		}},
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"Summary %q: p99 selected via quantile label; avg by (%s) collapses replicas.",
			gcPauseMetricName, strings.Join(group, ", "),
		),
	}
}

// buildHistogramPanel emits p50/p95/p99 queries via histogram_quantile,
// mirroring the service_http_latency approach.
func (serviceGCPauseRecipe) buildHistogramPanel(m ClassifiedMetricView) ir.Panel {
	percentiles := []struct {
		quantile float64
		legend   string
	}{
		{0.50, "p50"},
		{0.95, "p95"},
		{0.99, "p99"},
	}
	group := safeGroupLabels(m)
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
			Unit:         "s",
		})
	}
	return ir.Panel{
		Title:      fmt.Sprintf("GC pause: %s", gcPauseMetricName),
		Kind:       ir.PanelKindTimeSeries,
		Unit:       "s",
		Queries:    queries,
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"Histogram %q: p50/p95/p99 via histogram_quantile over %s with le grouping.",
			gcPauseMetricName, defaultRateWindow,
		),
	}
}
