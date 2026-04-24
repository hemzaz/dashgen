package recipes

// service_grpc_errors — non-OK RPC rate panel for the service profile.
//
// Operator question: how many gRPC calls per second are failing (grpc_code
// != "OK"), broken down by method?
//
// Signals:
//   - Classifier trait TraitServiceGRPC.
//   - MetricType counter.
//   - grpc_code label present on the descriptor.
// The grpc_code label must exist for the 5xx-equivalent filter to be
// meaningful. Without it the resulting query would be identical to the rate
// recipe.
//
// Confidence 0.85.
//
// Known look-alike that must NOT match:
//   - Counters carrying grpc_* in the name but lacking grpc_code (e.g., a
//     client-side retries_total counter). Requiring grpc_code-label
//     presence is the guard.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type serviceGRPCErrorsRecipe struct{}

// NewServiceGRPCErrors returns the service_grpc_errors recipe.
func NewServiceGRPCErrors() Recipe { return &serviceGRPCErrorsRecipe{} }

func (serviceGRPCErrorsRecipe) Name() string    { return "service_grpc_errors" }
func (serviceGRPCErrorsRecipe) Section() string { return "errors" }

// Match requires the service_grpc trait *and* a concrete grpc_code label to
// build the non-OK filter.
func (r serviceGRPCErrorsRecipe) Match(m ClassifiedMetricView) bool {
	return m.Type == inventory.MetricTypeCounter &&
		m.HasTrait("service_grpc") &&
		m.HasLabel("grpc_code")
}

func (r serviceGRPCErrorsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
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
			`sum by (%s) (rate(%s{grpc_code!="OK"}[%s]))`,
			strings.Join(group, ", "), m.Descriptor.Name, defaultRateWindow,
		)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("gRPC error rate: %s", m.Descriptor.Name),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "reqps",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: legendFor(group),
				Unit:         "reqps",
			}},
			Confidence: 0.85,
			Rationale: fmt.Sprintf(
				"Counter %q has a grpc_code label; filtering to grpc_code!=\"OK\" gives the RPC error rate.",
				m.Descriptor.Name,
			),
		})
	}
	sort.SliceStable(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
