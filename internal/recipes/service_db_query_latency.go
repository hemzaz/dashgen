package recipes

// service_db_query_latency — p50/p95/p99 DB query latency.
//
// Operator question: how long are database queries taking?
//
// Signals:
//   - MetricType histogram.
//   - TraitLatencyHistogram (from classify: name contains duration/latency
//     and the histogram series is well-formed).
//   - Metric name contains "query", "db", or "sql" (case-insensitive
//     substring match) as positive domain evidence.
//   - NOT TraitServiceHTTP — metrics with HTTP-shape labels belong to
//     service_http_latency, not this recipe.
//   - NOT TraitServiceGRPC — metrics with gRPC-shape labels belong to
//     service_grpc_latency, not this recipe.
//
// Grouping: {instance, job, le} — no per-statement grouping because
// statement text is too high-cardinality for a generic recipe.
//
// Confidence 0.75: lower than service_http_latency (0.75 is the same
// nominal band, but justified differently — name-based discrimination is
// softer than label-based discrimination). The HTTP/gRPC exclusion guards
// are the critical correctness gate; the name match is a domain filter.
//
// Known look-alikes that must NOT match:
//   - HTTP request duration histograms (have service_http trait).
//   - gRPC handling duration histograms (have service_grpc trait).
//   - Notification latency histograms that lack any of query/db/sql in
//     the name (no positive domain evidence → rejected).
//   - Counters whose name contains "query" (wrong metric type).
//   - Histograms without the latency_histogram trait (wrong shape).

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceDBQueryLatencyRecipe struct{}

// NewServiceDBQueryLatency returns the service_db_query_latency recipe.
func NewServiceDBQueryLatency() Recipe { return &serviceDBQueryLatencyRecipe{} }

func (serviceDBQueryLatencyRecipe) Name() string    { return "service_db_query_latency" }
func (serviceDBQueryLatencyRecipe) Section() string { return "latency" }

// Match requires a histogram with the latency_histogram trait whose name
// contains at least one of "query", "db", or "sql" (case-insensitive), and
// that carries neither the service_http nor service_grpc trait. The name
// check provides positive domain evidence; the trait exclusions prevent
// stealing metrics that already have a more specific recipe.
func (r serviceDBQueryLatencyRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeHistogram {
		return false
	}
	if !m.HasTrait("latency_histogram") {
		return false
	}
	if m.HasTrait("service_http") || m.HasTrait("service_grpc") {
		return false
	}
	lower := strings.ToLower(m.Descriptor.Name)
	return strings.Contains(lower, "query") ||
		strings.Contains(lower, "db") ||
		strings.Contains(lower, "sql")
}

func (r serviceDBQueryLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
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
		// Grouping is instance + job only — no per-statement label because
		// statement text is too high-cardinality. "le" is required for
		// histogram_quantile and injected explicitly.
		group := safeGroupLabels(m)
		group = ensureLabel(group, "le")
		// Prometheus metadata API returns histogram base names (without
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
			Title:      fmt.Sprintf("DB query latency (p50/p95/p99): %s", m.Descriptor.Name),
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.75,
			Rationale: fmt.Sprintf(
				"DB query latency histogram %q; p50/p95/p99 via histogram_quantile over %s with le grouping.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
