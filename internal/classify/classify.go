// Package classify runs deterministic classification over a MetricInventory.
//
// Classification is purely a function of the input inventory. No network,
// no clocks, no randomness. The output is a ClassifiedInventory that wraps
// the (already-sorted) inventory together with per-metric annotations.
//
// Rules are evaluated in a fixed order; the first matching rule wins. The
// rule set is intentionally conservative: if nothing matches, the metric is
// classified as unknown and downstream recipes are expected to skip it
// rather than fabricate a panel.
//
// Rule order (first match wins):
//  1. Explicit Prometheus metadata type (counter/gauge/histogram/summary)
//     when present on the descriptor.
//  2. Suffix-based fallback:
//     - "_bucket", "_sum", "_count" co-existing on the same base name
//     classify the "_bucket" descriptor as histogram.
//     - "_total" -> counter
//     - "_seconds" -> gauge with unit "s"
//     - "_bytes"   -> gauge with unit "bytes"
//     - "_ratio"   -> gauge with unit "percentunit"
//  3. Anything else stays MetricTypeUnknown.
//
// Family inference: the prefix up to the first "_" is the family (e.g.
// "http_requests_total" -> "http"). Empty names get family "".
//
// Traits:
//   - TraitServiceHTTP is attached when labels include any of
//     {method, status_code, route, path}.
//   - TraitLatencyHistogram is attached to a "_bucket" descriptor whose base
//     name contains "duration" or "latency" and whose labels include "le".
//
// Help-text hints (V0.2-PLAN Phase 1 item 3): after all label/name signals
// have been considered, helpHints inspects MetricDescriptor.Help with strict
// deterministic regexes. Hints are LOW-weight: they may ADD a trait that no
// label/name signal produced, but they MUST NOT override existing label
// evidence. If a contradicting trait is already present (e.g. the metric
// already carries TraitServiceGRPC), an HTTP hint from help text is
// suppressed. The patterns are intentionally narrow ("HTTP request", "gRPC
// call", etc.) so a help string that merely *mentions* the word in passing
// (e.g. "no grpc_method labels", "not HTTP status codes") does not trigger.
package classify

import (
	"regexp"
	"strings"

	"dashgen/internal/inventory"
)

// Trait is a classification hint downstream recipes can match on.
type Trait string

const (
	// TraitServiceHTTP marks metrics that look like HTTP service telemetry
	// based on labels such as method/status_code/route/path.
	TraitServiceHTTP Trait = "service_http"

	// TraitServiceGRPC marks metrics that look like gRPC service telemetry
	// based on labels grpc_{method,service,type,code}. gRPC and HTTP are
	// orthogonal: a metric may carry both traits (e.g., a gateway counter),
	// but recipes are specific to one or the other.
	TraitServiceGRPC Trait = "service_grpc"

	// TraitLatencyHistogram marks histogram _bucket descriptors whose base
	// name strongly suggests latency/duration semantics.
	TraitLatencyHistogram Trait = "latency_histogram"
)

// ClassifiedMetric is a MetricDescriptor plus the annotations classify added.
//
// The underlying descriptor is carried by value so mutations in later stages
// do not silently mutate the caller's inventory.
type ClassifiedMetric struct {
	Descriptor inventory.MetricDescriptor
	Type       inventory.MetricType
	Family     string
	Unit       string
	Traits     []Trait
}

// HasTrait reports whether the metric carries the given trait.
func (c ClassifiedMetric) HasTrait(t Trait) bool {
	for _, existing := range c.Traits {
		if existing == t {
			return true
		}
	}
	return false
}

// ClassifiedInventory is the inventory plus per-metric classification.
//
// Metrics retains the same stable sort order (by Name) as the input inventory.
type ClassifiedInventory struct {
	Inventory *inventory.MetricInventory
	Metrics   []ClassifiedMetric
}

// Classify applies the deterministic rule set over inv and returns a
// ClassifiedInventory. The input is not mutated.
func Classify(inv *inventory.MetricInventory) *ClassifiedInventory {
	if inv == nil {
		return &ClassifiedInventory{Inventory: &inventory.MetricInventory{}}
	}

	// Build the set of base names that have the full histogram trio
	// (_bucket + _sum + _count) so we can classify _bucket as histogram
	// only when the family is complete. This avoids guessing.
	hasSuffix := map[string]map[string]bool{}
	for _, m := range inv.Metrics {
		base, suffix := splitSuffix(m.Name)
		if suffix == "" {
			continue
		}
		if _, ok := hasSuffix[base]; !ok {
			hasSuffix[base] = map[string]bool{}
		}
		hasSuffix[base][suffix] = true
	}
	isHistogramBucket := func(name string) bool {
		base, suffix := splitSuffix(name)
		if suffix != "bucket" {
			return false
		}
		s := hasSuffix[base]
		return s["sum"] && s["count"]
	}

	out := &ClassifiedInventory{
		Inventory: inv,
		Metrics:   make([]ClassifiedMetric, 0, len(inv.Metrics)),
	}
	for _, m := range inv.Metrics {
		cm := classifyOne(m, isHistogramBucket)
		out.Metrics = append(out.Metrics, cm)
	}
	return out
}

// classifyOne applies the first-match-wins rule set to a single descriptor.
func classifyOne(m inventory.MetricDescriptor, isHistogramBucket func(string) bool) ClassifiedMetric {
	cm := ClassifiedMetric{
		Descriptor: m,
		Type:       inventory.MetricTypeUnknown,
		Family:     familyOf(m.Name),
	}

	// Rule 1: explicit Prometheus metadata wins when present.
	switch m.Type {
	case inventory.MetricTypeCounter,
		inventory.MetricTypeGauge,
		inventory.MetricTypeHistogram,
		inventory.MetricTypeSummary:
		cm.Type = m.Type
	}

	// Rule 2: suffix-based fallback when type is still unknown.
	if cm.Type == inventory.MetricTypeUnknown {
		switch {
		case isHistogramBucket(m.Name):
			cm.Type = inventory.MetricTypeHistogram
		case strings.HasSuffix(m.Name, "_total"):
			cm.Type = inventory.MetricTypeCounter
		case strings.HasSuffix(m.Name, "_seconds"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "s"
		case strings.HasSuffix(m.Name, "_bytes"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "bytes"
		case strings.HasSuffix(m.Name, "_ratio"):
			cm.Type = inventory.MetricTypeGauge
			cm.Unit = "percentunit"
		}
	}

	// Also apply unit hints when the descriptor was typed by metadata but
	// carries a unit-bearing suffix.
	if cm.Unit == "" {
		switch {
		case strings.HasSuffix(m.Name, "_seconds"):
			cm.Unit = "s"
		case strings.HasSuffix(m.Name, "_bytes"):
			cm.Unit = "bytes"
		case strings.HasSuffix(m.Name, "_ratio"):
			cm.Unit = "percentunit"
		}
	}

	// Trait: service HTTP hint. The label set covers both the
	// "{method,status_code,route,path}" convention and Go promhttp's
	// "{handler,code}" convention.
	if hasAnyLabel(m.Labels, "method", "status_code", "route", "path", "handler", "code") {
		cm.Traits = append(cm.Traits, TraitServiceHTTP)
	}

	// Trait: service gRPC hint. The canonical grpc-go server instrumentation
	// exposes grpc_method / grpc_service / grpc_type; grpc_code is added on
	// handled counters. Any one of these is enough to infer gRPC shape.
	if hasAnyLabel(m.Labels, "grpc_method", "grpc_service", "grpc_type", "grpc_code") {
		cm.Traits = append(cm.Traits, TraitServiceGRPC)
	}

	// Trait: latency histogram. Two paths:
	//   1. Name ends in "_bucket" with the full _sum+_count trio AND has `le`.
	//   2. Type was set to histogram by metadata (no _bucket suffix on name).
	// A name is "latency-shaped" if it either mentions duration/latency or
	// ends in a time-unit suffix (_seconds / _milliseconds). The latter
	// catches grpc_server_handling_seconds and other canonical server
	// histograms whose names don't contain the word "duration".
	switch {
	case isHistogramBucket(m.Name):
		base, _ := splitSuffix(m.Name)
		if looksLikeLatencyByName(base) && hasLabel(m.Labels, "le") {
			cm.Traits = append(cm.Traits, TraitLatencyHistogram)
		}
	case cm.Type == inventory.MetricTypeHistogram:
		// Exclude _sum/_count/_bucket partials. The bare base name
		// (returned by Prometheus's metadata API) is what we want to
		// trait; recipes append _bucket when synthesizing the query.
		if !strings.HasSuffix(m.Name, "_sum") &&
			!strings.HasSuffix(m.Name, "_count") &&
			!strings.HasSuffix(m.Name, "_bucket") &&
			looksLikeLatencyByName(m.Name) {
			cm.Traits = append(cm.Traits, TraitLatencyHistogram)
		}
	}

	// Help-text hints (LOW weight). Applied AFTER label/name signals so
	// help text never overrides label evidence. Hints can ADD a trait
	// when no signal disagrees, but MUST NOT override an existing
	// label/name decision. Help text is unreliable across exporters
	// (V0.2-PLAN §7), which is why this stage is gated by contradictions.
	for _, hint := range helpHints(m.Help) {
		applyHelpHint(&cm, hint, m.Labels)
	}

	return cm
}

// applyHelpHint adds a help-text-derived trait to cm only when:
//  1. The trait is not already present (avoid duplicates), and
//  2. No contradicting trait is present (label evidence wins), and
//  3. For service_http / service_grpc hints: the metric carries no
//     non-infrastructure labels. A label outside the {instance, job, le}
//     set is itself domain evidence — e.g. a `db_query` or `query_type`
//     label points to a DB-shape metric and must not be overridden by a
//     help string that merely mentions HTTP. The infrastructure-label
//     allowlist is intentionally narrow: any unlisted label suppresses
//     the hint.
//
// service_http and service_grpc are mutually exclusive for the purposes of
// help-text hinting: a metric labelled as gRPC must not be re-tagged HTTP
// from a help string that merely mentions HTTP, and vice versa.
func applyHelpHint(cm *ClassifiedMetric, hint Trait, labels []string) {
	if cm.HasTrait(hint) {
		return
	}
	switch hint {
	case TraitServiceHTTP:
		if cm.HasTrait(TraitServiceGRPC) {
			return
		}
		if hasNonInfraLabel(labels) {
			return
		}
	case TraitServiceGRPC:
		if cm.HasTrait(TraitServiceHTTP) {
			return
		}
		if hasNonInfraLabel(labels) {
			return
		}
	}
	cm.Traits = append(cm.Traits, hint)
}

// hasNonInfraLabel reports whether any label is outside the
// "infrastructure-only" allowlist. Infrastructure labels are the keys
// every Prometheus series carries regardless of domain shape: instance
// (the scrape target), job (the scrape config), and le (the histogram
// bucket bound). Anything else — method, route, status_code, handler,
// code, db_query, query_type, grpc_*, queue, host, ... — is positive
// domain evidence and must take precedence over a help-text hint.
func hasNonInfraLabel(labels []string) bool {
	for _, l := range labels {
		switch l {
		case "instance", "job", "le":
			continue
		default:
			return true
		}
	}
	return false
}

// familyOf returns the prefix up to (but not including) the first "_". For
// a name with no underscore, the whole name is the family.
func familyOf(name string) string {
	if name == "" {
		return ""
	}
	i := strings.Index(name, "_")
	if i < 0 {
		return name
	}
	return name[:i]
}

// splitSuffix splits a metric name into (base, suffix) on the last "_"
// when the trailing token is one of the recognized histogram/counter
// suffixes. Anything else returns ("", "").
func splitSuffix(name string) (string, string) {
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return "", ""
	}
	suffix := name[i+1:]
	switch suffix {
	case "bucket", "sum", "count", "total":
		return name[:i], suffix
	}
	return "", ""
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func hasAnyLabel(labels []string, wanted ...string) bool {
	for _, w := range wanted {
		if hasLabel(labels, w) {
			return true
		}
	}
	return false
}

// looksLikeLatencyByName returns true if the metric name suggests a latency
// histogram either by containing the words "duration"/"latency" or by having
// a time-unit suffix (_seconds, _milliseconds). The time-unit heuristic is
// what catches grpc_server_handling_seconds and other canonical histograms
// whose names don't literally say "duration".
func looksLikeLatencyByName(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "duration") || strings.Contains(lower, "latency") {
		return true
	}
	if strings.HasSuffix(lower, "_seconds") || strings.HasSuffix(lower, "_milliseconds") {
		return true
	}
	return false
}

// helpHelpTextHTTP matches narrow phrasings that strongly imply an HTTP
// service metric: "HTTP request", "HTTP response", "HTTP server", "HTTP
// client", "HTTP call". The whitespace requirement is what makes the
// pattern strict — strings like "no HTTP-shape labels" or "not HTTP
// status codes" do not match because the next token is not one of the
// listed nouns. Case-insensitive via the lowercased input.
var helpHelpTextHTTP = regexp.MustCompile(`\bhttp\s+(request|response|server|client|call)`)

// helpHelpTextGRPC matches narrow phrasings that strongly imply a gRPC
// service metric: "gRPC call", "gRPC request", "gRPC server", "gRPC
// client", "gRPC response", "RPC call", "RPC request" (this last pair
// catches help strings that abbreviate as "RPCs" but only when followed
// by a service-shape noun). The pattern intentionally requires
// whitespace so that "no grpc_method labels" — where "grpc" is glued
// to an underscore — does not trigger.
var helpHelpTextGRPC = regexp.MustCompile(`\b(grpc|rpc)\s+(call|request|response|server|client)`)

// helpHints returns LOW-weight trait hints derived from the metric
// descriptor's Help text. The output is order-independent (always
// emitted in the same canonical order: HTTP, gRPC) and the function
// performs no I/O, no goroutine creation, and no context usage — it
// is a pure substring/regex scan over its input.
//
// TraitLatencyHistogram is intentionally NOT emitted here. That trait
// is structural (must be a "_bucket" descriptor with an "le" label);
// promoting it from help text alone would add the latency trait to
// the accompanying _count and _sum series of the same histogram,
// which would cause downstream recipes to emit spurious panels.
//
// Help text is unreliable across exporters (V0.2-PLAN §7 risk), so
// callers must apply these hints only AFTER label/name signals and
// only when no contradicting signal is present. See applyHelpHint.
func helpHints(help string) []Trait {
	if help == "" {
		return nil
	}
	lower := strings.ToLower(help)
	var hints []Trait
	if helpHelpTextHTTP.MatchString(lower) {
		hints = append(hints, TraitServiceHTTP)
	}
	if helpHelpTextGRPC.MatchString(lower) {
		hints = append(hints, TraitServiceGRPC)
	}
	return hints
}
