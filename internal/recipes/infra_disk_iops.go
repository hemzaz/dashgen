// Package recipes – infra_disk_iops recipe.
//
// # Operator question
//
// How many disk read and write operations per second is each block device
// handling? A sustained climb in IOPS toward the device's rated limit signals
// saturation and predicts latency spikes before they become visible to
// applications.
//
// # Canonical signals
//
// Two node_exporter counters, either or both may be present:
//
//	node_disk_reads_completed_total  – cumulative completed read operations
//	node_disk_writes_completed_total – cumulative completed write operations
//
// Labels always include {instance, device}.
//
// # Aggregation shape
//
// sum by (instance, device) (rate(<metric>[5m]))
//
// rate() converts the monotonic counter into a per-second value (IOPS).
// sum by (instance, device) collapses any duplicate series that node_exporter
// may emit when multiple scrape paths are configured, while preserving the
// per-device breakdown that operators need to identify the hot disk.
//
// # Confidence: 0.85
//
// The two metric names are exact matches within the node_exporter disk family.
// Name equality alone does not reach 0.90 because structurally similar counters
// (node_disk_read_bytes_total, node_disk_write_bytes_total) exist in the same
// family; the _completed_ suffix is the discriminating token that makes 0.85
// the right level.
//
// # Known look-alikes that must NOT match
//
//   - node_disk_read_bytes_total / node_disk_write_bytes_total – byte-throughput
//     counters matched by the infra_disk recipe (raw avail/size), not IOPS.
//   - node_filesystem_size_bytes – filesystem gauge, entirely different family.
//   - Any gauge with a name that happens to contain "disk" – type guard rejects
//     these before the name check.
package recipes

import (
	"fmt"
	"sort"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// diskIOPSMetrics is the fixed set of node_exporter IOPS counters this recipe
// handles, together with human-readable panel titles.
var diskIOPSMetrics = []struct {
	name  string
	title string
}{
	{"node_disk_reads_completed_total", "Disk read IOPS"},
	{"node_disk_writes_completed_total", "Disk write IOPS"},
}

// diskIOPSNames is the same set keyed for O(1) lookup during Match.
var diskIOPSNames = func() map[string]bool {
	m := make(map[string]bool, len(diskIOPSMetrics))
	for _, e := range diskIOPSMetrics {
		m[e.name] = true
	}
	return m
}()

type infraDiskIOPSRecipe struct{}

// NewInfraDiskIOPS returns the infra_disk_iops recipe.
func NewInfraDiskIOPS() Recipe { return &infraDiskIOPSRecipe{} }

func (infraDiskIOPSRecipe) Name() string    { return "infra_disk_iops" }
func (infraDiskIOPSRecipe) Section() string { return "disk" }

func (r infraDiskIOPSRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	return diskIOPSNames[m.Descriptor.Name]
}

func (r infraDiskIOPSRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}

	// Collect which counters are actually present in the inventory.
	present := make(map[string]bool, len(diskIOPSMetrics))
	for _, m := range inv.Metrics {
		if diskIOPSNames[m.Descriptor.Name] {
			present[m.Descriptor.Name] = true
		}
	}
	if len(present) == 0 {
		return nil
	}

	var panels []ir.Panel
	for _, e := range diskIOPSMetrics {
		if !present[e.name] {
			continue
		}
		panels = append(panels, r.iopsPanel(e.title, e.name))
	}

	// Stable intra-recipe ordering: sort panels by title.
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

func (r infraDiskIOPSRecipe) iopsPanel(title, metric string) ir.Panel {
	expr := fmt.Sprintf(
		`sum by (instance, device) (rate(%s[%s]))`,
		metric, defaultRateWindow,
	)
	return ir.Panel{
		Title: title,
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "iops",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{device}}",
			Unit:         "iops",
		}},
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"node_exporter counter %q; rate over %s per instance+device.",
			metric, defaultRateWindow,
		),
	}
}
