package recipes

// infra_conntrack — Conntrack table saturation
//
// Operator question:
//
//	How close is the netfilter connection-tracking table to its configured
//	limit? A value approaching 1.0 means new connections will start being
//	dropped, causing hard-to-debug packet loss.
//
// Canonical signals:
//
//	node_nf_conntrack_entries       – gauge, current number of tracked flows
//	node_nf_conntrack_entries_limit – gauge, maximum allowed tracked flows
//	Both are emitted by node_exporter with an {instance} label.
//
// Aggregation shape:
//
//	node_nf_conntrack_entries / node_nf_conntrack_entries_limit
//	No sum-by needed: each node_exporter instance emits one value for each
//	metric; the raw ratio already scopes to {instance}.
//
// Confidence: 0.90 — both metric names are exact matches in the canonical
// node_exporter exposition; no name-prefix ambiguity exists.
//
// Known look-alikes that must not match:
//
//	node_nf_conntrack_entries_limit alone — not the primary signal.
//	Any counter with "conntrack" in the name but not this exact pair.

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type infraConntrackRecipe struct{}

// NewInfraConntrack returns the infra_conntrack recipe.
func NewInfraConntrack() Recipe { return &infraConntrackRecipe{} }

func (infraConntrackRecipe) Name() string    { return "infra_conntrack" }
func (infraConntrackRecipe) Section() string { return "saturation" }

// Match returns true only for the primary signal: node_nf_conntrack_entries.
// The limit metric is the second half of the pair but is not itself the
// trigger; BuildPanels will require both to be present.
func (r infraConntrackRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "node_nf_conntrack_entries"
}

func (r infraConntrackRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var hasEntries, hasLimit bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "node_nf_conntrack_entries":
			hasEntries = true
		case "node_nf_conntrack_entries_limit":
			hasLimit = true
		}
	}
	if !hasEntries || !hasLimit {
		return nil
	}
	expr := "node_nf_conntrack_entries / node_nf_conntrack_entries_limit"
	return []ir.Panel{{
		Title: "Conntrack table saturation",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.90,
		Rationale: fmt.Sprintf(
			"node_exporter conntrack gauges: entries / entries_limit yields table fill ratio per instance.",
		),
	}}
}
