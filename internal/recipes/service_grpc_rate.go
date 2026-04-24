package recipes

// service_grpc_rate — RPC throughput panel for the service profile.
//
// Operator question: how many gRPC calls per second is this service handling,
// broken down by method?
//
// Signals:
//   - Classifier trait TraitServiceGRPC (any of grpc_method/grpc_service/
//     grpc_type/grpc_code labels present).
//   - MetricType counter.
// Grouping: instance, job, grpc_service, grpc_method (whichever exist;
// banned-label filtering applies as for every other recipe).
//
// Confidence 0.85. Equal weight with service_http_rate because the gRPC
// server-side counters are just as canonical as promhttp counters.
//
// Known look-alike that must NOT match:
//   - Counters named grpc_* that lack both grpc_method and grpc_service
//     labels — often internal client-side retry counters that happen to
//     carry a grpc_code label. The recipe relies on the trait, which only
//     fires when a gRPC label is present, so requiring the trait is the
//     guard. The discrimination test enforces this against a
//     grpc_client_retries_total fixture metric.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceGRPCRateRecipe struct{}

// NewServiceGRPCRate returns the service_grpc_rate recipe.
func NewServiceGRPCRate() Recipe { return &serviceGRPCRateRecipe{} }

func (serviceGRPCRateRecipe) Name() string    { return "service_grpc_rate" }
func (serviceGRPCRateRecipe) Section() string { return "traffic" }

// Match requires a counter carrying the service_grpc trait. The broader
// service_http trait alone is not enough — some gateways expose both, and
// we specifically want to differentiate gRPC panels from HTTP panels so
// the rate section reads coherently.
func (r serviceGRPCRateRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeCounter && m.HasTrait("service_grpc")
}

func (r serviceGRPCRateRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}
	var panels []ir.Panel
	for _, m := range inv.Metrics {
		if !r.Match(m) {
			continue
		}
		group := safeGroupLabels(m, "grpc_service", "grpc_method")
		expr := fmt.Sprintf(
			"sum by (%s) (rate(%s[%s]))",
			strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow,
		)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("gRPC call rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"Counter %q carries gRPC-shape labels; rate over %s grouped by %s.",
				m.Descriptor.Name, defaultRateWindow, strings.Join(group, ", "),
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
