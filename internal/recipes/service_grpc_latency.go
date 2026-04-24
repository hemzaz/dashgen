package recipes

// service_grpc_latency — p50/p95/p99 RPC handling latency.
//
// Operator question: how long are RPC handlers taking, broken down by method?
//
// Signals:
//   - MetricType histogram.
//   - TraitLatencyHistogram (from classify: name contains duration/latency
//     and either it's a _bucket with the full trio or the metadata-typed
//     base name without a partial suffix).
//   - TraitServiceGRPC (from classify: any gRPC-shape label on the metric).
// Grouping: instance, job, grpc_service, grpc_method, le.
//
// Confidence 0.85.
//
// Known look-alikes that must NOT match:
//   - Non-gRPC latency histograms (internal op durations). The
//     service_grpc trait requirement is the guard; without one of
//     grpc_* labels, the trait never fires.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceGRPCLatencyRecipe struct{}

// NewServiceGRPCLatency returns the service_grpc_latency recipe.
func NewServiceGRPCLatency() Recipe { return &serviceGRPCLatencyRecipe{} }

func (serviceGRPCLatencyRecipe) Name() string    { return "service_grpc_latency" }
func (serviceGRPCLatencyRecipe) Section() string { return "latency" }

// Match requires a histogram with BOTH latency_histogram and service_grpc
// traits. Mirrors the service_http_latency guard against non-HTTP duration
// histograms — here we additionally partition by RPC family so gRPC
// handlers don't leak into the HTTP latency panel or vice versa.
func (r serviceGRPCLatencyRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeHistogram &&
		m.HasTrait("latency_histogram") &&
		m.HasTrait("service_grpc")
}

func (r serviceGRPCLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
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
		group := safeGroupLabels(m, "grpc_service", "grpc_method")
		group = ensureLabel(group, "le")
		// Same rule as service_http_latency: if the metric came from
		// metadata with the bare base name, append _bucket for the
		// queryable series.
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
			Title:      fmt.Sprintf("gRPC latency (p50/p95/p99): %s", m.Descriptor.Name),
			Kind:       ir.PanelKindTimeSeries,
			Unit:       "s",
			Queries:    queries,
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"gRPC latency histogram %q; p50/p95/p99 via histogram_quantile over %s with grpc_service/grpc_method + le grouping.",
				m.Descriptor.Name, defaultRateWindow,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
