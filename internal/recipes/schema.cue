// Package recipes defines the v0.3 dashgen recipe schema.
//
// This file is the SOURCE OF TRUTH for recipe structure. Every YAML recipe
// loaded by dashgen — built-in or user-supplied — must unify with #Recipe.
// Unification failures surface to the user as positional errors at file:line:col
// (loader, Phase 1A).
//
// Companion documents:
//   docs/RECIPES-DSL.md           — DSL specification (§4 = this schema).
//   docs/RECIPES-DSL-ADVERSARY.md — threat model (T1–T20) and invariants.
//   docs/V0.3-PLAN.md             — implementation plan (Phase 0 = this file).
//
// Threat-mitigation notes use the form:
//   // ADVERSARY: T<n> — <one-line description>
//
// Constraints that CUE cannot statically enforce (recursion-depth bounds,
// per-recipe predicate-node budgets, render-time output caps) are documented
// here in comments and re-validated by the Go loader in Phase 1A as
// defense-in-depth.

package recipes

import (
	"list"
	"strings"
)

// =============================================================================
// Reusable string constraints
// =============================================================================

// #MetricNameASCII restricts a string to 7-bit ASCII. Prometheus metric names
// per the exposition-format spec are ASCII; this constraint catches Cyrillic
// homoglyphs (e.g. U+0435 looking like Latin 'e') and other unicode lookalikes
// that silently break match predicates.
//
// Applied to every string the loader treats as a metric-name shape:
//   name_equals, name_equals_any[*], name_has_prefix, name_has_suffix,
//   name_contains, name_contains_any[*], name_matches,
//   pair_with.suffix_swap.{from,to}_suffix,
//   pair_with.prefix_swap.{from,to}_prefix,
//   pair_with.explicit.name.
//
// ADVERSARY: T20 — implicit unicode normalization / homoglyph confusion.
#MetricNameASCII: =~"^[\\x00-\\x7f]+$"

// #RegexPattern is a Go regular expression that the loader compiles and caches.
// Length is capped to bound parse-time abuse. Go's regexp is RE2-based, so
// runtime ReDoS via backtracking is structurally impossible — the cap is
// defense-in-depth against pathologically long patterns.
//
// ADVERSARY: T4 — regex denial-of-service. Length cap is the schema-level
//                 mitigation; the linear-time match guarantee comes from RE2.
#RegexPattern: #MetricNameASCII & strings.MaxRunes(256)

// #RateWindow is a Prometheus duration string used as the `rate(...)` window.
#RateWindow: =~"^[0-9]+(s|m|h|d)$"

// =============================================================================
// Top-level Recipe
// =============================================================================

// #Recipe is the root document type. Every recipe YAML is a value that must
// unify with this definition. The schema follows the YAML wire format defined
// in DSL §5.1: identity fields are nested under `metadata:`, with the body
// (match, panels, optional pair_with) at the top level.
#Recipe: {
	// ADVERSARY: T16 — apiVersion downgrade. Exact match; missing or unknown
	//                  apiVersion fails unification with a clear loader error.
	apiVersion: "dashgen.io/v1"
	kind:       "Recipe"

	metadata: #Metadata

	// Optional pair-presence resolver (multi-metric joins, Tier-C only).
	pair_with?: #PairSpec

	// Match predicate — what metrics this recipe fires on.
	match: #MatchPredicate

	// ADVERSARY: T13 — recipe panel-fan-out cap. A recipe must emit ≥1 panel
	//                  template and ≤16 (the per-recipe ceiling). Excess is
	//                  rejected at load.
	panels: [...#PanelTemplate] & list.MinItems(1) & list.MaxItems(16)
}

#Metadata: {
	name:    =~"^[a-z][a-z0-9_]*$" & strings.MaxRunes(64)
	section: #Section
	profile: "service" | "infra" | "k8s"

	// ADVERSARY: T14 — confidence-score gaming. Bounded to [0.0, 1.0]; values
	//                  outside this range are rejected at unification.
	confidence: >=0.0 & <=1.0

	tier: "v0.1" | "v0.2-T1" | "v0.2-T2" | "v0.2-T3" | "v0.3"

	description?: string & strings.MaxRunes(280)
	tags?: [...string]
}

// #Section is the closed set of dashboard sections a recipe may target.
#Section: "overview" | "traffic" | "errors" | "latency" |
	"saturation" | "cpu" | "memory" | "disk" | "network" |
	"pods" | "workloads" | "resources"

// =============================================================================
// Match predicate
// =============================================================================

// #MatchPredicate is a disjunction of primitive (leaf) and logical (combinator)
// predicates. CUE selects the matching arm based on which fields are present.
#MatchPredicate: #PrimitivePredicate | #LogicalPredicate

// #TraitName is the closed set of classifier-emitted traits that recipes may
// reference. Adding a new trait requires a coordinated change to
// internal/classify and this schema in the same PR.
#TraitName: "service_http" | "service_grpc" | "latency_histogram"

#PrimitivePredicate: {
	type?: "counter" | "gauge" | "histogram" | "summary"

	// Name shape. ADVERSARY: T7 — the seven name fields are mutually exclusive;
	// a primitive may declare AT MOST ONE. The mutex is enforced by the
	// hidden _name_predicate_count constraint below.
	name_equals?:       #MetricNameASCII
	name_equals_any?:   [...#MetricNameASCII] & list.MinItems(1)
	name_has_prefix?:   #MetricNameASCII
	name_has_suffix?:   #MetricNameASCII
	name_contains?:     #MetricNameASCII
	name_contains_any?: [...#MetricNameASCII] & list.MinItems(1)
	name_matches?:      #RegexPattern

	// Trait predicates.
	any_trait?: [...#TraitName] & list.MinItems(1)
	all_traits?: [...#TraitName] & list.MinItems(1)
	none_trait?: [...#TraitName] & list.MinItems(1)

	// Label predicates — label NAMES only, never values. Allowing label values
	// in the matcher would leak high-cardinality data into the determinism
	// surface (DSL §6.4, ADVERSARY invariant I2).
	has_label?: string
	has_label_any?: [...string] & list.MinItems(1)
	has_label_all?: [...string] & list.MinItems(1)
	has_label_none?: [...string] & list.MinItems(1)

	// ADVERSARY: T7 — name-predicate mutex.
	// At most one of the seven name-shape fields may be set per primitive.
	// CUE's list comprehension over optional fields concretizes the count
	// once each field's defined-ness is decidable; the unification then
	// constrains it ≤1. The Phase 1A loader re-validates this at decode
	// for defense in depth (and to surface a friendlier error).
	_name_predicate_count: len([
		if name_equals != _|_ {1},
		if name_equals_any != _|_ {1},
		if name_has_prefix != _|_ {1},
		if name_has_suffix != _|_ {1},
		if name_contains != _|_ {1},
		if name_contains_any != _|_ {1},
		if name_matches != _|_ {1},
	])
	_name_predicate_count: <=1
}

#LogicalPredicate: {
	// ADVERSARY: T7 — predicate-tree size limits.
	//   • depth cap: 8 levels of any_of/all_of/not nesting.
	//   • node-count cap: 64 total predicate nodes per recipe.
	// CUE recursion is unbounded by definition; both caps are enforced in the
	// Phase 1A loader by walking the decoded predicate AST. Documented here
	// as the schema's intent so reviewers can cross-reference.
	any_of?: [...#MatchPredicate] & list.MinItems(2)
	all_of?: [...#MatchPredicate] & list.MinItems(2)
	not?:    #MatchPredicate
}

// =============================================================================
// Panel template
// =============================================================================

#PanelTemplate: {
	// ADVERSARY: T5 — template parse-bomb mitigation. Per-template size caps
	//                 bound parse-time work. AST-node budget + forbidden-
	//                 directive walk are loader-side (Phase 1A T1A.3).
	title_template: string & strings.MaxRunes(160)
	kind:           "timeseries" | "stat" | "gauge" | "barchart" | *"timeseries"
	unit:           #Unit
	query_template: string & strings.MaxRunes(2048)

	// ADVERSARY: T6 — render-time output cap is enforced at render (loader).
	legend_template: string & strings.MaxRunes(160)

	// Optional explicit grouping. If omitted, helper safeGroupLabels() is
	// invoked at render time with the metric's natural label set.
	group_by?: [...string]

	// Optional preferred labels for safeGroupLabels(). Merged with
	// {job, instance} (always included if present).
	preferred_labels?: [...string]

	// Optional override of the rate window. Default is "5m" (applied by the
	// helper namespace, not by CUE — the field is simply absent here).
	rate_window?: #RateWindow

	// Histogram-specific: which quantiles to emit when this panel uses
	// histogram_quantile. Capped at 5 to bound per-panel fan-out.
	quantiles?: [...>=0.0 & <=1.0] & list.MinItems(1) & list.MaxItems(5)

	// Pair-dependent: this panel only emits if the pair was resolved.
	requires_pair?: bool | *false

	// Type-dispatch: panel only emits when the matched metric is of this type.
	requires_metric_type?: "counter" | "gauge" | "histogram" | "summary"
}

// #Unit is the canonical unit set; the trailing `string` arm is the vendor-
// specific fallback. The loader emits a WARN when a non-canonical unit is
// used so the catalog can canonicalize over time (DSL §16).
#Unit: "ops/sec" | "errors/sec" | "seconds" | "bytes" | "bytes/sec" |
	"ratio" | "percent" | "short" | "iops" | "count" | "days" | string

// =============================================================================
// Pair specification (Tier-C multi-metric joins)
// =============================================================================

// #PairSpec declares a multi-metric join. The join surface is bounded by
// design: only three modes (suffix_swap, prefix_swap, explicit). No general-
// purpose join is exposed (DSL §8.3).
//
// The mode mutex is encoded structurally as a disjunction over three closed
// shapes — exactly one of {suffix_swap, prefix_swap, explicit} must be set.
// CUE keeps the disjunction abstract under standalone `cue vet` and resolves
// it at unification time when concrete data lands.
#PairSpec: {
	// What to do when the pair candidate is absent from the inventory.
	on_missing: "omit" | "warn" | "use_first_only" | *"omit"
} & ({
	suffix_swap: #SuffixSwap
} | {
	prefix_swap: #PrefixSwap
} | {
	explicit: #ExplicitPair
})

#SuffixSwap: {
	from_suffix: #MetricNameASCII
	to_suffix:   #MetricNameASCII
}

#PrefixSwap: {
	from_prefix: #MetricNameASCII
	to_prefix:   #MetricNameASCII
}

#ExplicitPair: {
	name: #MetricNameASCII
}

// =============================================================================
// Composition definitions (DSL §4.5)
// =============================================================================
//
// These are reusable constraint sets a concrete recipe may unify against to
// inherit shared shape requirements (e.g. "histogram quantile recipes always
// emit timeseries panels in seconds"). Built-in YAML recipes don't have to
// declare the composition in the YAML — the loader can apply Unify with the
// matching composition as a structural check post-decode.

#HistogramQuantileRecipe: #Recipe & {
	match: type: "histogram"
	panels: [...{
		kind: "timeseries"
		unit: "seconds"
	}]
}

#PairRatioRecipe: #Recipe & {
	// pair_with becomes required (overrides the optional in #Recipe).
	pair_with: #PairSpec
	panels: [{requires_pair: true}, ...]
}

#NodeExporterRecipe: #Recipe & {
	metadata: profile: "infra"
	match: any_of: [{name_has_prefix: "node_"}, ...]
	panels: [...{preferred_labels: ["instance", "device", "mountpoint"]}]
}

#KubeStateRecipe: #Recipe & {
	metadata: profile: "k8s"
	match: any_of: [{name_has_prefix: "kube_"}, ...]
	panels: [...{preferred_labels: ["namespace", "pod"]}]
}
