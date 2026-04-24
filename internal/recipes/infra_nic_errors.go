// Package recipes – infra_nic_errors recipe.
//
// # Operator question
//
// Are any network interfaces dropping or erroring packets? Even a low but
// non-zero rate of NIC errors/drops indicates driver issues, hardware faults,
// or buffer exhaustion that will eventually affect application traffic.
//
// # Canonical signals
//
// Four node_exporter counters, any subset may be present:
//
//	node_network_receive_errs_total   – frames received with errors
//	node_network_transmit_errs_total  – frames transmitted with errors
//	node_network_receive_drop_total   – inbound frames dropped by the kernel
//	node_network_transmit_drop_total  – outbound frames dropped by the kernel
//
// Labels always include {instance, device}.
//
// # Aggregation shape
//
// sum by (instance, device) (rate(<metric>[5m]))
//
// rate() converts the monotonic counter into a per-second value. sum by
// (instance, device) collapses any duplicate series that node_exporter may
// emit when multiple scrape paths are present, while keeping the per-device
// breakdown that operators need to identify the offending interface.
//
// # Confidence: 0.85
//
// The four metric names are exact matches in the node_exporter family. Name
// equality alone is not quite 0.90 because the bytes/packets counters from the
// same family (node_network_receive_bytes_total, etc.) look structurally
// identical; the discrimination is the _errs_ / _drop_ infix, which is
// specific enough for 0.85.
//
// # Known look-alikes that must NOT match
//
//   - node_network_receive_bytes_total / node_network_transmit_bytes_total –
//     matched by the existing infra_network recipe (throughput, not errors).
//   - node_network_receive_packets_total / node_network_transmit_packets_total –
//     packet rate, not error/drop counters.
package recipes

import (
	"fmt"
	"sort"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// nicErrorMetrics is the fixed set of node_exporter error/drop counters this
// recipe handles, together with human-readable panel titles.
var nicErrorMetrics = []struct {
	name  string
	title string
}{
	{"node_network_receive_errs_total", "NIC RX errors"},
	{"node_network_receive_drop_total", "NIC RX drops"},
	{"node_network_transmit_errs_total", "NIC TX errors"},
	{"node_network_transmit_drop_total", "NIC TX drops"},
}

// nicErrorNames is the same set keyed for O(1) lookup during Match.
var nicErrorNames = func() map[string]bool {
	m := make(map[string]bool, len(nicErrorMetrics))
	for _, e := range nicErrorMetrics {
		m[e.name] = true
	}
	return m
}()

type infraNICErrorsRecipe struct{}

// NewInfraNICErrors returns the infra_nic_errors recipe.
func NewInfraNICErrors() Recipe { return &infraNICErrorsRecipe{} }

func (infraNICErrorsRecipe) Name() string    { return "infra_nic_errors" }
func (infraNICErrorsRecipe) Section() string { return "network" }

func (r infraNICErrorsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	return nicErrorNames[m.Descriptor.Name]
}

func (r infraNICErrorsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}

	// Collect which of the four counters are actually present in the inventory.
	present := make(map[string]bool, len(nicErrorMetrics))
	for _, m := range inv.Metrics {
		if nicErrorNames[m.Descriptor.Name] {
			present[m.Descriptor.Name] = true
		}
	}
	if len(present) == 0 {
		return nil
	}

	var panels []ir.Panel
	for _, e := range nicErrorMetrics {
		if !present[e.name] {
			continue
		}
		panels = append(panels, r.errorPanel(e.title, e.name))
	}

	// Stable intra-recipe ordering: sort panels by title.
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

func (r infraNICErrorsRecipe) errorPanel(title, metric string) ir.Panel {
	expr := fmt.Sprintf(
		`sum by (instance, device) (rate(%s[%s]))`,
		metric, defaultRateWindow,
	)
	return ir.Panel{
		Title: title,
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "cps",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{device}}",
			Unit:         "cps",
		}},
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"node_exporter counter %q; rate over %s per instance+device.",
			metric, defaultRateWindow,
		),
	}
}
