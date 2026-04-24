// Package discover — FixtureSource provides an offline discovery + query
// backend for golden tests and CI runs. It reads metadata, series, optional
// per-metric label names, and instant-query responses from a directory on
// disk and exposes them via the same Source and prometheus.Client interfaces
// as the live backend.
//
// Layout (all files JSON, unknown-field-strict off):
//
//	<dir>/metadata.json       map[string][]MetricMetadata
//	<dir>/series.json         []map[string]string
//	<dir>/labelnames/<m>.json []string      (optional)
//	<dir>/instant/<sha>.json  prometheus.QueryResult
//
// Instant-query lookup hashes the expression with SHA-256 and looks for
// `<hex[:16]>.json` under `instant/`. Missing files yield an empty
// QueryResult so the validate pipeline sees the stage-3 "empty_result"
// warning rather than an error.
package discover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"dashgen/internal/prometheus"
)

// FixtureSource is a Source backed by a directory of JSON files.
type FixtureSource struct {
	metadata   map[string][]prometheus.MetricMetadata
	series     []map[string]string
	labelNames map[string][]string
	instant    map[string]prometheus.QueryResult
}

// NewFixtureSource loads a fixture directory fully into memory and returns a
// source that can answer Discover() calls and back a FixtureClient.
func NewFixtureSource(dir string) (*FixtureSource, error) {
	if dir == "" {
		return nil, fmt.Errorf("fixture: dir required")
	}
	meta, err := loadMetadata(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	series, err := loadSeries(filepath.Join(dir, "series.json"))
	if err != nil {
		return nil, err
	}
	labelNames, err := loadLabelNames(filepath.Join(dir, "labelnames"))
	if err != nil {
		return nil, err
	}
	instant, err := loadInstant(filepath.Join(dir, "instant"))
	if err != nil {
		return nil, err
	}
	return &FixtureSource{
		metadata:   meta,
		series:     series,
		labelNames: labelNames,
		instant:    instant,
	}, nil
}

// Discover returns a RawInventory sourced from the loaded fixture. It mirrors
// the determinism contract of PrometheusSource: metrics sorted by name,
// labels sorted per metric.
func (s *FixtureSource) Discover(_ context.Context, sel Selector) (*RawInventory, error) {
	if s == nil {
		return nil, fmt.Errorf("fixture: nil source")
	}
	names := make([]string, 0, len(s.metadata))
	for name := range s.metadata {
		if !matchesSelector(name, sel) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	raw := &RawInventory{Metrics: make([]RawMetric, 0, len(names))}
	for _, name := range names {
		m := RawMetric{Name: name}
		if entries := s.metadata[name]; len(entries) > 0 {
			m.Type = entries[0].Type
			m.Help = entries[0].Help
			m.Unit = entries[0].Unit
		}
		// Mirror PrometheusSource: for a histogram whose metadata entry
		// carries the base name, label discovery has to look at _bucket
		// series (the base name has no queryable series).
		labelTarget := name
		if m.Type == "histogram" && !hasHistogramPartialSuffix(name) {
			if labels := s.labelsForMetric(name + "_bucket"); len(labels) > 0 {
				labelTarget = name + "_bucket"
			}
		}
		labels := s.labelsForMetric(labelTarget)
		if len(labels) > 0 {
			m.Labels = labels
		}
		raw.Metrics = append(raw.Metrics, m)
	}
	return raw, nil
}

// hasHistogramPartialSuffix reports whether the metric name already ends in
// one of the histogram partial suffixes, in which case no _bucket fallback
// is needed.
func hasHistogramPartialSuffix(name string) bool {
	for _, suf := range []string{"_bucket", "_sum", "_count"} {
		if len(name) > len(suf) && name[len(name)-len(suf):] == suf {
			return true
		}
	}
	return false
}

// labelsForMetric returns the sorted union of:
//   - labels from labelnames/<metric>.json (if present)
//   - labels observed on any series entry whose __name__ matches.
//
// The job label is filtered by Selector.Job if provided, but only for series
// discovery; the resulting label-name set is the same regardless of selector.
func (s *FixtureSource) labelsForMetric(metric string) []string {
	seen := map[string]bool{}
	for _, l := range s.labelNames[metric] {
		seen[l] = true
	}
	for _, series := range s.series {
		if series["__name__"] != metric {
			continue
		}
		for k := range series {
			if k == "__name__" {
				continue
			}
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FixtureClient implements prometheus.Client against the same files as
// FixtureSource. Queries that are not pre-recorded return an empty
// QueryResult; the validator treats that as WarningEmptyResult rather than
// an execution error.
type FixtureClient struct {
	src *FixtureSource
}

// NewFixtureClient wraps a FixtureSource so it can be passed to the validate
// pipeline as a Prometheus client.
func NewFixtureClient(src *FixtureSource) *FixtureClient {
	return &FixtureClient{src: src}
}

// Metadata returns the raw metadata map loaded from metadata.json.
func (c *FixtureClient) Metadata(_ context.Context) (map[string][]prometheus.MetricMetadata, error) {
	if c == nil || c.src == nil {
		return nil, fmt.Errorf("fixture: nil client")
	}
	out := make(map[string][]prometheus.MetricMetadata, len(c.src.metadata))
	for k, v := range c.src.metadata {
		cp := make([]prometheus.MetricMetadata, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out, nil
}

// LabelNames returns the sorted label-name set for the given metric.
func (c *FixtureClient) LabelNames(_ context.Context, metric string) ([]string, error) {
	if c == nil || c.src == nil {
		return nil, fmt.Errorf("fixture: nil client")
	}
	return c.src.labelsForMetric(metric), nil
}

// Series returns all label sets that match any of the provided metric names.
// The match[] syntax used by the real backend is not fully simulated here;
// fixture callers pass bare metric names.
func (c *FixtureClient) Series(_ context.Context, match []string) ([]map[string]string, error) {
	if c == nil || c.src == nil {
		return nil, fmt.Errorf("fixture: nil client")
	}
	want := map[string]bool{}
	for _, m := range match {
		want[m] = true
	}
	var out []map[string]string
	for _, s := range c.src.series {
		if want[s["__name__"]] {
			cp := make(map[string]string, len(s))
			for k, v := range s {
				cp[k] = v
			}
			out = append(out, cp)
		}
	}
	return out, nil
}

// InstantQuery hashes expr and looks up instant/<sha>.json. Missing entries
// yield an empty QueryResult so the validator emits WarningEmptyResult.
func (c *FixtureClient) InstantQuery(_ context.Context, expr string) (*prometheus.QueryResult, error) {
	if c == nil || c.src == nil {
		return nil, fmt.Errorf("fixture: nil client")
	}
	if expr == "" {
		return nil, fmt.Errorf("InstantQuery: expr required")
	}
	key := exprHash(expr)
	if qr, ok := c.src.instant[key]; ok {
		cp := qr
		return &cp, nil
	}
	// No pre-recorded result → empty vector, no warnings. Validator will
	// add WarningEmptyResult at stage 3.
	return &prometheus.QueryResult{ResultType: "vector"}, nil
}

// ExprHash returns the canonical hash key used by the fixture client to look
// up a pre-recorded instant-query response. Exposed so fixture authors can
// script response file generation.
func ExprHash(expr string) string { return exprHash(expr) }

func exprHash(expr string) string {
	sum := sha256.Sum256([]byte(expr))
	return hex.EncodeToString(sum[:])[:16]
}

func loadMetadata(path string) (map[string][]prometheus.MetricMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fixture: read metadata: %w", err)
	}
	var out map[string][]prometheus.MetricMetadata
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("fixture: parse metadata: %w", err)
	}
	return out, nil
}

func loadSeries(path string) ([]map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fixture: read series: %w", err)
	}
	var out []map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("fixture: parse series: %w", err)
	}
	return out, nil
}

func loadLabelNames(dir string) (map[string][]string, error) {
	out := map[string][]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("fixture: read labelnames dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		metric := name[:len(name)-len(".json")]
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("fixture: read labelnames/%s: %w", name, err)
		}
		var labels []string
		if err := json.Unmarshal(data, &labels); err != nil {
			return nil, fmt.Errorf("fixture: parse labelnames/%s: %w", name, err)
		}
		out[metric] = labels
	}
	return out, nil
}

func loadInstant(dir string) (map[string]prometheus.QueryResult, error) {
	out := map[string]prometheus.QueryResult{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("fixture: read instant dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		key := name[:len(name)-len(".json")]
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("fixture: read instant/%s: %w", name, err)
		}
		var qr prometheus.QueryResult
		if err := json.Unmarshal(data, &qr); err != nil {
			return nil, fmt.Errorf("fixture: parse instant/%s: %w", name, err)
		}
		out[key] = qr
	}
	return out, nil
}
