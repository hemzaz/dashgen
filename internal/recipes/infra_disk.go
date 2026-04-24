package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// infraDiskRecipe emits a disk-used-ratio panel derived from
// node_filesystem_avail_bytes and node_filesystem_size_bytes.
type infraDiskRecipe struct{}

// NewInfraDisk returns the infra_disk recipe.
func NewInfraDisk() Recipe { return &infraDiskRecipe{} }

func (infraDiskRecipe) Name() string    { return "infra_disk" }
func (infraDiskRecipe) Section() string { return "disk" }

func (r infraDiskRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	switch m.Descriptor.Name {
	case "node_filesystem_avail_bytes", "node_filesystem_size_bytes":
		return true
	}
	return false
}

func (r infraDiskRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	var hasAvail, hasSize bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "node_filesystem_avail_bytes":
			hasAvail = true
		case "node_filesystem_size_bytes":
			hasSize = true
		}
	}
	if !hasAvail || !hasSize {
		return nil
	}
	expr := "1 - (node_filesystem_avail_bytes / node_filesystem_size_bytes)"
	return []ir.Panel{{
		Title: "Disk used ratio",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{mountpoint}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.8,
		Rationale: fmt.Sprintf(
			"node_exporter filesystem gauges: 1 - (avail / size) yields used ratio grouped by instance+mountpoint.",
		),
	}}
}
