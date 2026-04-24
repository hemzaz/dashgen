// Package recipes – infra_disk_io_latency recipe.
//
// # Operator question
//
// How much of wall-clock time is each block device spending in active IO?
// A value approaching 1.0 (100%) means the device is saturated — any new IO
// must queue behind in-flight work, and application latency will spike.
//
// # Canonical signals
//
// Two node_exporter counters, either or both may be present:
//
//	node_disk_io_time_seconds_total          – cumulative seconds the device
//	                                           has spent doing IO (classic USE
//	                                           saturation signal; rate gives a
//	                                           fraction of wall-clock time)
//	node_disk_io_time_weighted_seconds_total – cumulative time weighted by
//	                                           queue depth (useful when many IOs
//	                                           are in-flight simultaneously)
//
// Labels always include {instance, device}.
//
// # Aggregation shape
//
// sum by (instance, device) (rate(<metric>[5m]))
//
// rate() converts the monotonic counter into a per-second value.  For
// node_disk_io_time_seconds_total the result is dimensionless (seconds of IO
// per second of wall-clock) — Grafana unit "percentunit" renders it as 0–100%.
// For node_disk_io_time_weighted_seconds_total the result retains the unit "s"
// (weighted seconds per second, i.e. average queue depth in seconds).
// sum by (instance, device) collapses duplicate series while preserving the
// per-device breakdown needed to identify the hot disk.
//
// # Confidence: 0.85
//
// The two metric names are exact matches within the node_exporter disk family.
// Name equality alone does not reach 0.90 because structurally similar counters
// (node_disk_reads_completed_total, node_disk_written_bytes_total) exist in the
// same family; the _io_time_ infix is the discriminating token.
//
// # Known look-alikes that must NOT match
//
//   - node_disk_reads_completed_total / node_disk_writes_completed_total – IOPS
//     counters matched by infra_disk_iops, not IO-time latency.
//   - node_disk_written_bytes_total – byte-throughput counter, different family.
//   - Any gauge whose name contains "disk" – type guard rejects these before
//     the name check.
package recipes

import (
	"fmt"
	"sort"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// diskIOLatencyMetrics is the fixed set of node_exporter IO-time counters this
// recipe handles, together with human-readable panel titles and Grafana units.
var diskIOLatencyMetrics = []struct {
	name  string
	title string
	unit  string
}{
	{"node_disk_io_time_seconds_total", "Disk IO busy fraction", "percentunit"},
	{"node_disk_io_time_weighted_seconds_total", "Disk IO weighted time", "s"},
}

// diskIOLatencyNames is the same set keyed for O(1) lookup during Match.
var diskIOLatencyNames = func() map[string]bool {
	m := make(map[string]bool, len(diskIOLatencyMetrics))
	for _, e := range diskIOLatencyMetrics {
		m[e.name] = true
	}
	return m
}()

type infraDiskIOLatencyRecipe struct{}

// NewInfraDiskIOLatency returns the infra_disk_io_latency recipe.
func NewInfraDiskIOLatency() Recipe { return &infraDiskIOLatencyRecipe{} }

func (infraDiskIOLatencyRecipe) Name() string    { return "infra_disk_io_latency" }
func (infraDiskIOLatencyRecipe) Section() string { return "disk" }

func (r infraDiskIOLatencyRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	return diskIOLatencyNames[m.Descriptor.Name]
}

func (r infraDiskIOLatencyRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}

	// Collect which counters are actually present in the inventory.
	present := make(map[string]bool, len(diskIOLatencyMetrics))
	for _, m := range inv.Metrics {
		if diskIOLatencyNames[m.Descriptor.Name] {
			present[m.Descriptor.Name] = true
		}
	}
	if len(present) == 0 {
		return nil
	}

	var panels []ir.Panel
	for _, e := range diskIOLatencyMetrics {
		if !present[e.name] {
			continue
		}
		panels = append(panels, r.ioLatencyPanel(e.title, e.name, e.unit))
	}

	// Stable intra-recipe ordering: sort panels by title.
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

func (r infraDiskIOLatencyRecipe) ioLatencyPanel(title, metric, unit string) ir.Panel {
	expr := fmt.Sprintf(
		`sum by (instance, device) (rate(%s[%s]))`,
		metric, defaultRateWindow,
	)
	return ir.Panel{
		Title: title,
		Kind:  ir.PanelKindTimeSeries,
		Unit:  unit,
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{device}}",
			Unit:         unit,
		}},
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"node_exporter counter %q; rate over %s per instance+device.",
			metric, defaultRateWindow,
		),
	}
}
