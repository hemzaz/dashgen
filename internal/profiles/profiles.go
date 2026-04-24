// Package profiles owns profile identity, panel caps, and section ordering.
//
// The v0.1 slice only implements the `service` profile, but the enum values
// for infra and k8s exist so that downstream packages can pattern-match on
// a single Profile type without importing stringly-typed constants.
package profiles

// Profile is a closed enum of dashboard profiles supported by DashGen.
type Profile string

const (
	ProfileService Profile = "service"
	ProfileInfra   Profile = "infra"
	ProfileK8s     Profile = "k8s"
)

// defaultPanelCap is the maximum number of panels any single profile may
// emit in v0.1. Conservative caps are part of the product contract; see
// PRODUCT_DOC.md.
const defaultPanelCap = 8

// IsKnown reports whether p is one of the closed profile values.
func IsKnown(p Profile) bool {
	switch p {
	case ProfileService, ProfileInfra, ProfileK8s:
		return true
	}
	return false
}

// PanelCap returns the maximum panel count for a profile.
//
// For v0.1 every profile shares the default. Future profiles may override.
func PanelCap(p Profile) int {
	_ = p
	return defaultPanelCap
}

// Sections returns the canonical section order for a profile.
//
// Determinism: this slice is the authoritative section order for rendering;
// callers must preserve it. The v0.1 service profile sections come from
// PRODUCT_DOC.md (overview/traffic/errors/latency/saturation).
func Sections(p Profile) []string {
	switch p {
	case ProfileService:
		return []string{"overview", "traffic", "errors", "latency", "saturation"}
	case ProfileInfra:
		return []string{"overview", "cpu", "memory", "disk", "network"}
	case ProfileK8s:
		return []string{"overview", "pods", "workloads", "resources"}
	}
	return nil
}
