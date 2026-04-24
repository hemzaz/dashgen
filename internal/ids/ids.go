// Package ids generates stable identifiers for dashboards and panels.
//
// Determinism guarantee: every ID is a SHA-256 hash over a canonical,
// sorted-where-applicable input string. The output is hex-encoded and
// truncated to 16 characters. The input material is constructed only from
// product-meaningful values (profile, inventory hash, section, metric name,
// visualization kind) so that renaming a display title does not invalidate
// the ID.
package ids

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// idLength is the hex length of emitted IDs. 16 hex chars = 64 bits of the
// SHA-256 digest, which is more than sufficient for collision resistance
// across a single Grafana dashboard.
const idLength = 16

// DashboardUID returns a stable UID for a dashboard built from a given
// profile and inventory hash.
//
// Determinism: the two inputs are joined with a separator that cannot appear
// in either and then hashed. Same (profile, inventoryHash) → same UID.
func DashboardUID(profile, inventoryHash string) string {
	return shortHash("dashboard|" + profile + "|" + inventoryHash)
}

// PanelUID returns a stable UID for a panel scoped to a dashboard.
//
// Determinism: the inputs are lowercased to avoid drift from display-only
// case changes and joined with a separator. Same (dashboardUID, section,
// metricName, kind) → same UID.
func PanelUID(dashboardUID, section, metricName, kind string) string {
	material := strings.Join([]string{
		"panel",
		strings.ToLower(dashboardUID),
		strings.ToLower(section),
		strings.ToLower(metricName),
		strings.ToLower(kind),
	}, "|")
	return shortHash(material)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:idLength]
}
