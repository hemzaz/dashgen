package recipes

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dashgen/internal/inventory"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// makeMetric builds a ClassifiedMetricView for use in matcher tests.
func makeMetric(name string, mtype inventory.MetricType, labels, traits []string) ClassifiedMetricView {
	return ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{
			Name:   name,
			Type:   mtype,
			Labels: labels,
		},
		Type:   mtype,
		Traits: traits,
	}
}

// ─── Eval — primitive predicates ────────────────────────────────────────────

func TestEval_Primitives(t *testing.T) {
	httpMetric := makeMetric(
		"http_requests_total",
		inventory.MetricTypeCounter,
		[]string{"method", "route", "status_code"},
		[]string{"service_http"},
	)
	histMetric := makeMetric(
		"http_request_duration_seconds",
		inventory.MetricTypeHistogram,
		[]string{"le"},
		[]string{"latency_histogram"},
	)
	gaugeMetric := makeMetric(
		"node_memory_usage_bytes",
		inventory.MetricTypeGauge,
		[]string{"instance"},
		[]string{},
	)

	cases := []struct {
		name   string
		pred   MatchPredicate
		metric ClassifiedMetricView
		want   bool
	}{
		// ── type ─────────────────────────────────────────────────────────────
		{
			name:   "type/counter match",
			pred:   MatchPredicate{Type: "counter"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "type/counter no-match (gauge)",
			pred:   MatchPredicate{Type: "counter"},
			metric: gaugeMetric,
			want:   false,
		},
		{
			name:   "type/histogram match",
			pred:   MatchPredicate{Type: "histogram"},
			metric: histMetric,
			want:   true,
		},
		{
			name:   "type/histogram no-match (counter)",
			pred:   MatchPredicate{Type: "histogram"},
			metric: httpMetric,
			want:   false,
		},

		// ── name_equals ───────────────────────────────────────────────────────
		{
			name:   "name_equals/match",
			pred:   MatchPredicate{NameEquals: "http_requests_total"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_equals/no-match",
			pred:   MatchPredicate{NameEquals: "http_requests_total"},
			metric: gaugeMetric,
			want:   false,
		},

		// ── name_equals_any ───────────────────────────────────────────────────
		{
			name:   "name_equals_any/first match",
			pred:   MatchPredicate{NameEqualsAny: []string{"http_requests_total", "other_metric"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_equals_any/second match",
			pred:   MatchPredicate{NameEqualsAny: []string{"other_metric", "http_requests_total"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_equals_any/no match",
			pred:   MatchPredicate{NameEqualsAny: []string{"foo", "bar"}},
			metric: httpMetric,
			want:   false,
		},

		// ── name_has_prefix ───────────────────────────────────────────────────
		{
			name:   "name_has_prefix/match",
			pred:   MatchPredicate{NameHasPrefix: "http_"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_has_prefix/no-match",
			pred:   MatchPredicate{NameHasPrefix: "node_"},
			metric: httpMetric,
			want:   false,
		},

		// ── name_has_suffix ───────────────────────────────────────────────────
		{
			name:   "name_has_suffix/match",
			pred:   MatchPredicate{NameHasSuffix: "_total"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_has_suffix/no-match",
			pred:   MatchPredicate{NameHasSuffix: "_total"},
			metric: gaugeMetric,
			want:   false,
		},

		// ── name_contains ─────────────────────────────────────────────────────
		{
			name:   "name_contains/match",
			pred:   MatchPredicate{NameContains: "request"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_contains/no-match",
			pred:   MatchPredicate{NameContains: "grpc"},
			metric: httpMetric,
			want:   false,
		},

		// ── name_contains_any ─────────────────────────────────────────────────
		{
			name:   "name_contains_any/match first",
			pred:   MatchPredicate{NameContainsAny: []string{"request", "grpc"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_contains_any/match second",
			pred:   MatchPredicate{NameContainsAny: []string{"grpc", "request"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_contains_any/no match",
			pred:   MatchPredicate{NameContainsAny: []string{"grpc", "kafka"}},
			metric: httpMetric,
			want:   false,
		},

		// ── name_matches ──────────────────────────────────────────────────────
		{
			name:   "name_matches/match",
			pred:   MatchPredicate{NameMatches: "^http_.*_total$"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "name_matches/no-match",
			pred:   MatchPredicate{NameMatches: "^grpc_"},
			metric: httpMetric,
			want:   false,
		},

		// ── any_trait ─────────────────────────────────────────────────────────
		{
			name:   "any_trait/match",
			pred:   MatchPredicate{AnyTrait: []string{"service_http", "service_grpc"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "any_trait/no-match",
			pred:   MatchPredicate{AnyTrait: []string{"service_grpc"}},
			metric: httpMetric,
			want:   false,
		},

		// ── all_traits ────────────────────────────────────────────────────────
		{
			name:   "all_traits/all present",
			pred:   MatchPredicate{AllTraits: []string{"latency_histogram"}},
			metric: histMetric,
			want:   true,
		},
		{
			name:   "all_traits/one missing",
			pred:   MatchPredicate{AllTraits: []string{"latency_histogram", "service_http"}},
			metric: histMetric,
			want:   false,
		},

		// ── none_trait ────────────────────────────────────────────────────────
		{
			name:   "none_trait/none present",
			pred:   MatchPredicate{NoneTrait: []string{"service_grpc"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "none_trait/one present",
			pred:   MatchPredicate{NoneTrait: []string{"service_http"}},
			metric: httpMetric,
			want:   false,
		},

		// ── has_label ─────────────────────────────────────────────────────────
		{
			name:   "has_label/present",
			pred:   MatchPredicate{HasLabel: "method"},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "has_label/absent",
			pred:   MatchPredicate{HasLabel: "namespace"},
			metric: httpMetric,
			want:   false,
		},

		// ── has_label_any ─────────────────────────────────────────────────────
		{
			name:   "has_label_any/one present",
			pred:   MatchPredicate{HasLabelAny: []string{"method", "namespace"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "has_label_any/none present",
			pred:   MatchPredicate{HasLabelAny: []string{"namespace", "pod"}},
			metric: httpMetric,
			want:   false,
		},

		// ── has_label_all ─────────────────────────────────────────────────────
		{
			name:   "has_label_all/all present",
			pred:   MatchPredicate{HasLabelAll: []string{"method", "route"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "has_label_all/one absent",
			pred:   MatchPredicate{HasLabelAll: []string{"method", "namespace"}},
			metric: httpMetric,
			want:   false,
		},

		// ── has_label_none ────────────────────────────────────────────────────
		{
			name:   "has_label_none/none present",
			pred:   MatchPredicate{HasLabelNone: []string{"namespace", "pod"}},
			metric: httpMetric,
			want:   true,
		},
		{
			name:   "has_label_none/one present",
			pred:   MatchPredicate{HasLabelNone: []string{"method"}},
			metric: httpMetric,
			want:   false,
		},

		// ── conjunctive fields ────────────────────────────────────────────────
		{
			name: "conjunctive/type+prefix both match",
			pred: MatchPredicate{
				Type:          "counter",
				NameHasPrefix: "http_",
			},
			metric: httpMetric,
			want:   true,
		},
		{
			name: "conjunctive/type matches but prefix fails",
			pred: MatchPredicate{
				Type:          "counter",
				NameHasPrefix: "grpc_",
			},
			metric: httpMetric,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pre-warm cache for any name_matches patterns.
			if err := ValidateBudget(tc.pred); err != nil {
				t.Fatalf("ValidateBudget: %v", err)
			}
			got := Eval(tc.pred, tc.metric)
			if got != tc.want {
				t.Errorf("Eval() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ─── Eval — logical combinators ─────────────────────────────────────────────

func TestEval_Combinators(t *testing.T) {
	counter := makeMetric("http_requests_total", inventory.MetricTypeCounter, []string{"method"}, []string{"service_http"})
	gauge := makeMetric("node_cpu_usage", inventory.MetricTypeGauge, []string{"instance"}, []string{})

	matchCounter := MatchPredicate{Type: "counter"}
	matchGauge := MatchPredicate{Type: "gauge"}
	matchHTTPPrefix := MatchPredicate{NameHasPrefix: "http_"}
	matchNodePrefix := MatchPredicate{NameHasPrefix: "node_"}

	cases := []struct {
		name   string
		pred   MatchPredicate
		metric ClassifiedMetricView
		want   bool
	}{
		// ── any_of ────────────────────────────────────────────────────────────
		{
			name:   "any_of/first matches",
			pred:   MatchPredicate{AnyOf: []MatchPredicate{matchCounter, matchGauge}},
			metric: counter,
			want:   true,
		},
		{
			name:   "any_of/second matches",
			pred:   MatchPredicate{AnyOf: []MatchPredicate{matchGauge, matchCounter}},
			metric: counter,
			want:   true,
		},
		{
			name:   "any_of/none match",
			pred:   MatchPredicate{AnyOf: []MatchPredicate{matchGauge, matchNodePrefix}},
			metric: counter,
			want:   false,
		},

		// ── all_of ────────────────────────────────────────────────────────────
		{
			name:   "all_of/all match",
			pred:   MatchPredicate{AllOf: []MatchPredicate{matchCounter, matchHTTPPrefix}},
			metric: counter,
			want:   true,
		},
		{
			name:   "all_of/first fails",
			pred:   MatchPredicate{AllOf: []MatchPredicate{matchGauge, matchHTTPPrefix}},
			metric: counter,
			want:   false,
		},
		{
			name:   "all_of/second fails",
			pred:   MatchPredicate{AllOf: []MatchPredicate{matchCounter, matchNodePrefix}},
			metric: counter,
			want:   false,
		},

		// ── not ───────────────────────────────────────────────────────────────
		{
			name:   "not/inner false → outer true",
			pred:   MatchPredicate{Not: &MatchPredicate{Type: "gauge"}},
			metric: counter,
			want:   true,
		},
		{
			name:   "not/inner true → outer false",
			pred:   MatchPredicate{Not: &MatchPredicate{Type: "counter"}},
			metric: counter,
			want:   false,
		},

		// ── nested combinators ────────────────────────────────────────────────
		{
			// any_of[ all_of[counter, http_prefix], gauge ]
			// counter+http metric → inner all_of matches → true
			name: "nested/any_of wrapping all_of — match via all_of arm",
			pred: MatchPredicate{
				AnyOf: []MatchPredicate{
					{AllOf: []MatchPredicate{matchCounter, matchHTTPPrefix}},
					matchGauge,
				},
			},
			metric: counter,
			want:   true,
		},
		{
			// any_of[ all_of[counter, http_prefix], gauge ]
			// gauge metric → inner all_of fails, matchGauge matches → true
			name: "nested/any_of wrapping all_of — match via gauge arm",
			pred: MatchPredicate{
				AnyOf: []MatchPredicate{
					{AllOf: []MatchPredicate{matchCounter, matchHTTPPrefix}},
					matchGauge,
				},
			},
			metric: gauge,
			want:   true,
		},
		{
			// not( any_of[counter, http_prefix] ) against counter → false
			name: "nested/not wrapping any_of — inner matches → outer false",
			pred: MatchPredicate{
				Not: &MatchPredicate{
					AnyOf: []MatchPredicate{matchCounter, matchHTTPPrefix},
				},
			},
			metric: counter,
			want:   false,
		},
		{
			// not( any_of[gauge, node_prefix] ) against counter → true
			name: "nested/not wrapping any_of — inner no-match → outer true",
			pred: MatchPredicate{
				Not: &MatchPredicate{
					AnyOf: []MatchPredicate{matchGauge, matchNodePrefix},
				},
			},
			metric: counter,
			want:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateBudget(tc.pred); err != nil {
				t.Fatalf("ValidateBudget: %v", err)
			}
			got := Eval(tc.pred, tc.metric)
			if got != tc.want {
				t.Errorf("Eval() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ─── ReDoS immunity ──────────────────────────────────────────────────────────

// TestEval_RedosImmunity verifies that matching a pathological polynomial-
// blowup regex against a long adversarial string completes in under 10ms.
// Go's RE2-backed regexp is linear-time, so this is always expected to pass.
// It acts as a canary: if the package ever switches to a backtracking engine,
// this test would time out (ADVERSARY T4).
func TestEval_RedosImmunity(t *testing.T) {
	// Classic polynomial regex: "(a+)+" would be O(2^n) on backtracking engines.
	// Go's RE2 handles it in O(n).
	pred := MatchPredicate{NameMatches: "(a+)+$"}
	if err := ValidateBudget(pred); err != nil {
		t.Fatalf("ValidateBudget: %v", err)
	}

	// 10 000-character adversarial input ending in a non-matching character.
	adversarial := strings.Repeat("a", 10000) + "!"
	m := makeMetric(adversarial, inventory.MetricTypeCounter, nil, nil)

	start := time.Now()
	result := Eval(pred, m)
	elapsed := time.Since(start)

	if result {
		t.Error("Eval should not match: adversarial string ends with '!' which breaks (a+)+$")
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("RE2 immunity violated: match took %v (want < 10ms)", elapsed)
	}
}

// ─── ValidateBudget — depth and node caps ────────────────────────────────────

// buildNestedNot returns a predicate nested n levels deep via not.
// Each node is allocated as a distinct slice element so that no Not pointer
// ever aliases its parent (a self-referential structure would cause
// validateBudgetRec to spin forever before hitting the depth cap).
func buildNestedNot(n int) MatchPredicate {
	nodes := make([]MatchPredicate, n+1)
	nodes[0] = MatchPredicate{Type: "counter"} // leaf
	for i := 1; i <= n; i++ {
		nodes[i] = MatchPredicate{Not: &nodes[i-1]}
	}
	return nodes[n]
}

// TestValidateBudget_DepthCap verifies that 8 levels of not-nesting (depth
// index 0..8, i.e. the leaf is at depth 8) is rejected with ErrPredicateTooDeep.
func TestValidateBudget_DepthCap(t *testing.T) {
	// buildNestedNot(8): root is at depth 0, innermost wrapped 8 times → leaf at depth 8.
	// depth >= maxPredicateDepth (8) triggers the error.
	deep := buildNestedNot(8)
	err := ValidateBudget(deep)
	if !errors.Is(err, ErrPredicateTooDeep) {
		t.Errorf("ValidateBudget(depth=8) = %v, want ErrPredicateTooDeep", err)
	}
}

func TestValidateBudget_JustBelowDepthCap(t *testing.T) {
	// 7 layers: leaf at depth 7 — within budget.
	ok := buildNestedNot(7)
	if err := ValidateBudget(ok); err != nil {
		t.Errorf("ValidateBudget(depth=7) unexpected error: %v", err)
	}
}

// TestValidateBudget_NodeCap verifies that 65 sibling primitive nodes inside
// an all_of are rejected with ErrPredicateTooMany (root + 65 children = 66 nodes).
func TestValidateBudget_NodeCap(t *testing.T) {
	children := make([]MatchPredicate, 65)
	for i := range children {
		children[i] = MatchPredicate{Type: "counter"}
	}
	pred := MatchPredicate{AllOf: children}
	err := ValidateBudget(pred)
	if !errors.Is(err, ErrPredicateTooMany) {
		t.Errorf("ValidateBudget(65 children) = %v, want ErrPredicateTooMany", err)
	}
}

func TestValidateBudget_JustBelowNodeCap(t *testing.T) {
	// 63 children + 1 root = 64 nodes — exactly at the limit (not over).
	children := make([]MatchPredicate, 63)
	for i := range children {
		children[i] = MatchPredicate{Type: "counter"}
	}
	pred := MatchPredicate{AllOf: children}
	if err := ValidateBudget(pred); err != nil {
		t.Errorf("ValidateBudget(63 children) unexpected error: %v", err)
	}
}

// TestValidateBudget_InvalidRegex verifies that an invalid Go regex pattern
// causes ValidateBudget to return an error (not a panic).
func TestValidateBudget_InvalidRegex(t *testing.T) {
	pred := MatchPredicate{NameMatches: "[invalid"}
	err := ValidateBudget(pred)
	if err == nil {
		t.Error("ValidateBudget([invalid) expected error, got nil")
	}
}
