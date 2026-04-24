// Package discover owns discovery workflows. It decides which backend calls
// are needed to build a RawInventory and bounds their cost.
package discover

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/prometheus"
)

// Selector narrows discovery to a subset of the backend.
type Selector struct {
	Job         string
	Namespace   string
	MetricMatch string
}

// RawInventory is the pre-normalization discovery result. It preserves the
// raw backend shapes so inventory/ can decide how to canonicalize them.
//
// Determinism: Metrics is sorted by name; Labels and Series for each metric
// are sorted before being returned.
type RawInventory struct {
	Metrics []RawMetric
}

// RawMetric holds per-metric raw discovery data.
type RawMetric struct {
	Name     string
	Type     string
	Help     string
	Unit     string
	Labels   []string
	Series   []map[string]string
	SampleOK bool
}

// Source is the interface implemented by discovery backends. PrometheusSource
// is the only v0.1 implementation.
type Source interface {
	Discover(ctx context.Context, sel Selector) (*RawInventory, error)
}

// PrometheusSource is a Source backed by a prometheus.Client.
type PrometheusSource struct {
	Client prometheus.Client
}

// NewPrometheusSource is a small constructor for symmetry with other
// packages. It exists to keep the zero value from being a useful state.
func NewPrometheusSource(c prometheus.Client) *PrometheusSource {
	return &PrometheusSource{Client: c}
}

// Discover queries the backend for metadata and per-metric label/series
// hints, then returns a deterministically ordered RawInventory.
//
// Determinism: metric names are sorted; per-metric labels are sorted; each
// series label set is returned in stable order.
func (s *PrometheusSource) Discover(ctx context.Context, sel Selector) (*RawInventory, error) {
	if s == nil || s.Client == nil {
		return nil, fmt.Errorf("discover: nil client")
	}
	meta, err := s.Client.Metadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}

	// Sorted metric name list — never iterate the metadata map directly.
	names := make([]string, 0, len(meta))
	for name := range meta {
		if !matchesSelector(name, sel) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	raw := &RawInventory{Metrics: make([]RawMetric, 0, len(names))}
	for _, name := range names {
		m := RawMetric{Name: name}
		if entries := meta[name]; len(entries) > 0 {
			m.Type = entries[0].Type
			m.Help = entries[0].Help
			m.Unit = entries[0].Unit
		}
		labels, lerr := s.Client.LabelNames(ctx, name)
		if lerr == nil {
			sort.Strings(labels)
			m.Labels = labels
		}
		raw.Metrics = append(raw.Metrics, m)
	}
	return raw, nil
}

func matchesSelector(name string, sel Selector) bool {
	if sel.MetricMatch == "" {
		return true
	}
	// Foundation-level substring match. Real regex handling is the
	// responsibility of the classification/recipes layer.
	return strings.Contains(name, sel.MetricMatch)
}
