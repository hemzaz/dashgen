// Package inventory defines the normalized metric inventory model and the
// canonical hashing rule used for dashboard identity.
package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// MetricType is the Prometheus type of a metric family.
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
	MetricTypeSummary   MetricType = "summary"
	MetricTypeUnknown   MetricType = "unknown"
)

// MetricDescriptor describes a single metric family and the inferred traits
// classification adds to it.
type MetricDescriptor struct {
	Name           string
	Type           MetricType
	Help           string
	Labels         []string
	InferredUnit   string
	InferredFamily string
	Sample         map[string]string
}

// MetricInventory is the canonical, deterministic view of a Prometheus
// instance suitable for classification and recipe matching.
//
// Determinism: Metrics MUST be kept sorted by Name. Callers must invoke
// Sort() after mutation and before user-visible use.
type MetricInventory struct {
	Metrics []MetricDescriptor
}

// Sort sorts the inventory in place by metric name and normalizes label
// ordering within each descriptor.
//
// Determinism: ascending lexical sort on Name is the authoritative order for
// every downstream consumer.
func (inv *MetricInventory) Sort() {
	if inv == nil {
		return
	}
	sort.Slice(inv.Metrics, func(i, j int) bool {
		return inv.Metrics[i].Name < inv.Metrics[j].Name
	})
	for i := range inv.Metrics {
		sort.Strings(inv.Metrics[i].Labels)
	}
}

// InventoryHash returns a SHA-256 hex digest (16 chars) over a canonical
// serialization of the inventory.
//
// Determinism: metrics are sorted first; labels are sorted; all fields are
// joined with delimiters that cannot appear in metric or label names.
func InventoryHash(inv *MetricInventory) string {
	if inv == nil {
		return hashString("empty")
	}
	// Copy then sort so we do not mutate the caller's inventory.
	copyInv := MetricInventory{Metrics: append([]MetricDescriptor(nil), inv.Metrics...)}
	copyInv.Sort()
	var sb strings.Builder
	for _, m := range copyInv.Metrics {
		sb.WriteString(m.Name)
		sb.WriteString("|")
		sb.WriteString(string(m.Type))
		sb.WriteString("|")
		sb.WriteString(m.InferredUnit)
		sb.WriteString("|")
		sb.WriteString(m.InferredFamily)
		sb.WriteString("|")
		sb.WriteString(strings.Join(m.Labels, ","))
		sb.WriteString("\n")
	}
	return hashString(sb.String())
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}
