package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// infraNetworkRecipe emits RX and TX throughput panels from the node_exporter
// network counters, excluding loopback and veth interfaces.
type infraNetworkRecipe struct{}

// NewInfraNetwork returns the infra_network recipe.
func NewInfraNetwork() Recipe { return &infraNetworkRecipe{} }

func (infraNetworkRecipe) Name() string    { return "infra_network" }
func (infraNetworkRecipe) Section() string { return "network" }

func (r infraNetworkRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	switch m.Descriptor.Name {
	case "node_network_receive_bytes_total", "node_network_transmit_bytes_total":
		return true
	}
	return false
}

func (r infraNetworkRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var hasRx, hasTx bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "node_network_receive_bytes_total":
			hasRx = true
		case "node_network_transmit_bytes_total":
			hasTx = true
		}
	}
	var panels []ir.Panel
	if hasRx {
		panels = append(panels, r.throughputPanel(
			"Network RX",
			"node_network_receive_bytes_total",
		))
	}
	if hasTx {
		panels = append(panels, r.throughputPanel(
			"Network TX",
			"node_network_transmit_bytes_total",
		))
	}
	return panels
}

// throughputPanel builds a single rate-per-interface panel. Loopback and
// veth devices are excluded so the graph reflects real host traffic.
func (r infraNetworkRecipe) throughputPanel(title, metric string) ir.Panel {
	expr := fmt.Sprintf(
		`sum by (instance, device) (rate(%s{device!~"lo|veth.*"}[%s]))`,
		metric, defaultRateWindow,
	)
	return ir.Panel{
		Title: fmt.Sprintf("%s: %s", title, metric),
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "Bps",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{device}}",
			Unit:         "Bps",
		}},
		Confidence: 0.8,
		Rationale: fmt.Sprintf(
			"node_exporter counter %q; rate over %s per instance+device, excluding lo and veth.*.",
			metric, defaultRateWindow,
		),
	}
}
