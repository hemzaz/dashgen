// Package recipes — infra_filesystem_usage
//
// Operator question: what percent of each filesystem is currently in use?
//
// Signals: the node_exporter gauge pair node_filesystem_size_bytes and
// node_filesystem_avail_bytes, both carrying {instance, mountpoint, fstype}
// labels. The recipe requires both metrics to be present in the snapshot;
// if either is absent it returns no panels (graceful omission).
//
// Query shape: (node_filesystem_size_bytes{fstype!=""} - node_filesystem_avail_bytes{fstype!=""})
// / node_filesystem_size_bytes{fstype!=""} grouped by {instance, mountpoint, fstype}.
// The subtraction is element-wise vector arithmetic; no sum() wrapper is
// needed because a given {instance, mountpoint, fstype} tuple uniquely
// identifies a single timeseries from each vector. The fstype!="" filter
// drops pseudo-filesystems (tmpfs, devtmpfs) that node_exporter sometimes
// exposes with an empty fstype string.
//
// Rationale for confidence 0.85: strong name + label signal (exact metric
// names from a well-known exporter), but fractionally below 0.90 because
// the same underlying metric pair is also consumed by infra_disk (which
// reports raw avail/size). These two recipes are complementary, not
// competing, but the slight overlap keeps the confidence honest.
//
// Look-alikes that must NOT match:
//   - node_filesystem_files / node_filesystem_files_free (inode counts, not bytes)
//   - kubelet_volume_stats_available_bytes (covered by k8s_pvc_usage)
//   - node_filesystem_device_error (gauge flag, not a bytes metric)
package recipes

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// infraFilesystemUsageRecipe emits a single filesystem-used-ratio panel
// derived from the node_filesystem_size_bytes / node_filesystem_avail_bytes pair.
type infraFilesystemUsageRecipe struct{}

// NewInfraFilesystemUsage returns the infra_filesystem_usage recipe.
func NewInfraFilesystemUsage() Recipe { return &infraFilesystemUsageRecipe{} }

func (infraFilesystemUsageRecipe) Name() string    { return "infra_filesystem_usage" }
func (infraFilesystemUsageRecipe) Section() string { return "disk" }

// Match returns true when the descriptor is node_filesystem_size_bytes (the
// size half of the pair). The avail counterpart is verified in BuildPanels
// by walking the full inventory snapshot.
func (r infraFilesystemUsageRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "node_filesystem_size_bytes"
}

func (r infraFilesystemUsageRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}

	var hasSize, hasAvail bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "node_filesystem_size_bytes":
			hasSize = true
		case "node_filesystem_avail_bytes":
			hasAvail = true
		}
	}
	if !hasSize || !hasAvail {
		return nil
	}

	const filter = `{fstype!=""}`
	expr := fmt.Sprintf(
		"(node_filesystem_size_bytes%s - node_filesystem_avail_bytes%s) / node_filesystem_size_bytes%s",
		filter, filter, filter,
	)

	return []ir.Panel{{
		Title: "Filesystem used ratio",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{mountpoint}} {{fstype}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.85,
		Rationale: "node_exporter filesystem gauge pair: " +
			"(size - avail) / size yields used ratio per {instance, mountpoint, fstype}.",
	}}
}
