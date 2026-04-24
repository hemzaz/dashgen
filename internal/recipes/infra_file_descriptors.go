package recipes

// infra_file_descriptors — fd saturation ratio
//
// Operator question: are we close to the process fd limit?
//
// Signals:
//   - process_open_fds (gauge): current number of open file descriptors.
//   - process_max_fds  (gauge): configured fd ceiling (ulimit -n).
//
// Both metrics are emitted by the Prometheus Go client and the node_exporter
// process-collector. They always arrive as a pair on any properly configured
// target.
//
// Aggregation: process_open_fds / process_max_fds, grouped by {instance, job}.
// No sum needed — each series already represents a single process. The ratio
// tells the operator what fraction of the fd budget is consumed; values above
// 0.80 warrant investigation.
//
// Confidence: 0.90 — both metric names are exact, canonical strings from the
// Prometheus client specification. No shape ambiguity exists.
//
// Section choice: overview.
// fd exhaustion is a cross-cutting saturation signal. It triggers failures
// across CPU work (goroutine scheduling), disk I/O (file opens fail), and
// network I/O (socket creation fails). It does not belong cleanly in the
// cpu, memory, disk, or network sections of the infra profile, so overview
// is the correct home — it is the section operators check first when
// something is wrong but the cause is not yet isolated.
//
// Look-alikes that must NOT match:
//   - node_filefd_allocated / node_filefd_maximum: kernel-level fd counters
//     from node_exporter; different namespace ("node_" not "process_") and
//     semantics (system-wide, not per-process). This recipe ignores them.

import (
	"fmt"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

type infraFileDescriptorsRecipe struct{}

// NewInfraFileDescriptors returns the infra_file_descriptors recipe.
func NewInfraFileDescriptors() Recipe { return &infraFileDescriptorsRecipe{} }

func (infraFileDescriptorsRecipe) Name() string    { return "infra_file_descriptors" }
func (infraFileDescriptorsRecipe) Section() string { return "overview" }

// Match fires on process_open_fds (the primary gauge that drives the ratio).
// process_max_fds is the supporting half of the pair; we do not match on it
// alone because BuildPanels keys off the open-fds side.
func (r infraFileDescriptorsRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	return m.Descriptor.Name == "process_open_fds"
}

func (r infraFileDescriptorsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileInfra {
		return nil
	}
	// Require both halves of the pair to avoid emitting a broken expression.
	var hasOpen, hasMax bool
	for _, m := range inv.Metrics {
		switch m.Descriptor.Name {
		case "process_open_fds":
			hasOpen = true
		case "process_max_fds":
			hasMax = true
		}
	}
	if !hasOpen || !hasMax {
		return nil
	}
	expr := "process_open_fds / process_max_fds"
	return []ir.Panel{{
		Title: "Open fds vs max",
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "percentunit",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: "{{instance}} {{job}}",
			Unit:         "percentunit",
		}},
		Confidence: 0.90,
		Rationale: fmt.Sprintf(
			"process_open_fds / process_max_fds: fd utilisation ratio per instance+job. " +
				"Values near 1.0 indicate imminent fd exhaustion.",
		),
	}}
}
