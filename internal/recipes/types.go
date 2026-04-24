// Package recipes defines the Recipe interface and the registry of known
// recipes. Concrete recipes live in sibling files in this package.
package recipes

import (
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// defaultRateWindow is the PromQL rate() window used by recipes that emit
// per-second series. Single source of truth so every panel is comparable.
const defaultRateWindow = "5m"

// DefaultRateWindow exposes the default rate window for tests and
// documentation consumers.
func DefaultRateWindow() string { return defaultRateWindow }

// ClassifiedMetricView is the minimal per-metric view recipes need without
// importing internal/classify (which would create an import cycle the moment
// classify begins to use recipe-specific traits).
type ClassifiedMetricView struct {
	Descriptor inventory.MetricDescriptor
	Type       inventory.MetricType
	Family     string
	Unit       string
	Traits     []string
}

// HasTrait reports whether the metric carries the given trait string.
func (c ClassifiedMetricView) HasTrait(t string) bool {
	for _, existing := range c.Traits {
		if existing == t {
			return true
		}
	}
	return false
}

// HasLabel reports whether the descriptor includes the given label.
func (c ClassifiedMetricView) HasLabel(name string) bool {
	for _, l := range c.Descriptor.Labels {
		if l == name {
			return true
		}
	}
	return false
}

// ClassifiedInventorySnapshot is the snapshot view passed to recipes when
// building panels. It is deliberately small: recipes should not reach for
// anything beyond what the snapshot exposes.
type ClassifiedInventorySnapshot struct {
	Inventory *inventory.MetricInventory
	Metrics   []ClassifiedMetricView
}

// Recipe builds zero or more panels for a profile out of a classified
// inventory. Recipes must be pure functions of their inputs.
type Recipe interface {
	// Name returns a stable identifier for the recipe. Used in rationale
	// output and for deterministic tie-breaking between competing recipes.
	Name() string

	// Section returns the dashboard section this recipe contributes to.
	// The return value must be one of profiles.Sections(profile).
	Section() string

	// Match reports whether the recipe applies to the given descriptor in
	// isolation. Matching is cheap and intended to run per-metric.
	Match(m ClassifiedMetricView) bool

	// BuildPanels constructs panels for the profile using the full
	// classified inventory. Returning nil is the correct behavior when no
	// confident panel can be produced; weak panels must not be invented.
	//
	// Panels returned here MUST carry UID = "" — synth fills in the panel
	// UID after it has computed the dashboard UID. This keeps UID material
	// centralized in one place.
	BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel
}
