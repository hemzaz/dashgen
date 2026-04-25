package classify

import (
	"runtime"
	"testing"

	"dashgen/internal/inventory"
)

func TestClassify_TableDriven(t *testing.T) {
	type want struct {
		typ    inventory.MetricType
		family string
		unit   string
		traits []Trait
	}
	tests := []struct {
		name       string
		descriptor inventory.MetricDescriptor
		extra      []inventory.MetricDescriptor // sibling metrics needed for histogram detection
		want       want
	}{
		{
			name: "explicit_counter_metadata_wins",
			descriptor: inventory.MetricDescriptor{
				Name: "http_requests_total",
				Type: inventory.MetricTypeCounter,
			},
			want: want{typ: inventory.MetricTypeCounter, family: "http"},
		},
		{
			name: "explicit_gauge_metadata_wins_even_with_bytes_suffix",
			descriptor: inventory.MetricDescriptor{
				Name: "process_resident_memory_bytes",
				Type: inventory.MetricTypeGauge,
			},
			want: want{typ: inventory.MetricTypeGauge, family: "process", unit: "bytes"},
		},
		{
			name: "suffix_total_classifies_as_counter",
			descriptor: inventory.MetricDescriptor{
				Name: "kafka_messages_total",
			},
			want: want{typ: inventory.MetricTypeCounter, family: "kafka"},
		},
		{
			name: "suffix_seconds_classifies_as_gauge_with_unit",
			descriptor: inventory.MetricDescriptor{
				Name: "node_boot_time_seconds",
			},
			want: want{typ: inventory.MetricTypeGauge, family: "node", unit: "s"},
		},
		{
			name: "suffix_bytes_classifies_as_gauge_with_unit",
			descriptor: inventory.MetricDescriptor{
				Name: "go_memstats_alloc_bytes",
			},
			want: want{typ: inventory.MetricTypeGauge, family: "go", unit: "bytes"},
		},
		{
			name: "suffix_ratio_classifies_as_gauge_percentunit",
			descriptor: inventory.MetricDescriptor{
				Name: "cache_hit_ratio",
			},
			want: want{typ: inventory.MetricTypeGauge, family: "cache", unit: "percentunit"},
		},
		{
			name: "histogram_bucket_with_full_trio",
			descriptor: inventory.MetricDescriptor{
				Name:   "http_request_duration_seconds_bucket",
				Labels: []string{"le", "method"},
			},
			extra: []inventory.MetricDescriptor{
				{Name: "http_request_duration_seconds_sum"},
				{Name: "http_request_duration_seconds_count"},
			},
			want: want{
				typ:    inventory.MetricTypeHistogram,
				family: "http",
				traits: []Trait{TraitServiceHTTP, TraitLatencyHistogram},
			},
		},
		{
			name: "service_http_trait_from_route_label",
			descriptor: inventory.MetricDescriptor{
				Name:   "api_requests_total",
				Labels: []string{"route"},
			},
			want: want{
				typ:    inventory.MetricTypeCounter,
				family: "api",
				traits: []Trait{TraitServiceHTTP},
			},
		},
		{
			name: "unknown_when_no_rule_matches",
			descriptor: inventory.MetricDescriptor{
				Name: "weird_thing",
			},
			want: want{typ: inventory.MetricTypeUnknown, family: "weird"},
		},
		{
			name: "bucket_without_sum_and_count_is_not_histogram",
			descriptor: inventory.MetricDescriptor{
				Name:   "stray_bucket",
				Labels: []string{"le"},
			},
			want: want{typ: inventory.MetricTypeUnknown, family: "stray"},
		},
		{
			name: "service_http_trait_from_handler_label",
			descriptor: inventory.MetricDescriptor{
				Name:   "promhttp_metric_handler_requests_total",
				Labels: []string{"code", "handler"},
			},
			want: want{
				typ:    inventory.MetricTypeCounter,
				family: "promhttp",
				traits: []Trait{TraitServiceHTTP},
			},
		},
		{
			name: "latency_trait_on_bare_histogram_metadata",
			descriptor: inventory.MetricDescriptor{
				Name:   "prometheus_http_request_duration_seconds",
				Type:   inventory.MetricTypeHistogram,
				Labels: []string{"handler"},
			},
			want: want{
				typ:    inventory.MetricTypeHistogram,
				family: "prometheus",
				unit:   "s",
				traits: []Trait{TraitServiceHTTP, TraitLatencyHistogram},
			},
		},
		{
			name: "latency_trait_excludes_count_partial",
			descriptor: inventory.MetricDescriptor{
				Name:   "prometheus_http_request_duration_seconds_count",
				Type:   inventory.MetricTypeHistogram,
				Labels: []string{"handler"},
			},
			want: want{
				typ:    inventory.MetricTypeHistogram,
				family: "prometheus",
				traits: []Trait{TraitServiceHTTP},
			},
		},
		{
			name: "latency_trait_excludes_sum_partial",
			descriptor: inventory.MetricDescriptor{
				Name:   "prometheus_http_request_duration_seconds_sum",
				Type:   inventory.MetricTypeHistogram,
				Labels: []string{"handler"},
			},
			want: want{
				typ:    inventory.MetricTypeHistogram,
				family: "prometheus",
				traits: []Trait{TraitServiceHTTP},
			},
		},
		{
			name: "grpc_trait_from_grpc_method_and_service_labels",
			descriptor: inventory.MetricDescriptor{
				Name:   "grpc_server_handled_total",
				Type:   inventory.MetricTypeCounter,
				Labels: []string{"grpc_code", "grpc_method", "grpc_service"},
			},
			want: want{
				typ:    inventory.MetricTypeCounter,
				family: "grpc",
				traits: []Trait{TraitServiceGRPC},
			},
		},
		{
			name: "grpc_latency_histogram_gets_both_traits",
			descriptor: inventory.MetricDescriptor{
				Name:   "grpc_server_handling_seconds",
				Type:   inventory.MetricTypeHistogram,
				Labels: []string{"grpc_method", "grpc_service", "le"},
			},
			want: want{
				typ:    inventory.MetricTypeHistogram,
				family: "grpc",
				unit:   "s",
				traits: []Trait{TraitServiceGRPC, TraitLatencyHistogram},
			},
		},
		{
			// Regression guard: a counter named grpc_* with no grpc_*
			// labels must NOT get the service_grpc trait — this is the
			// "internal client retries counter" look-alike class named in
			// RECIPES.md §3.1.1.
			name: "grpc_named_counter_without_grpc_labels_has_no_grpc_trait",
			descriptor: inventory.MetricDescriptor{
				Name:   "grpc_client_retries_total",
				Type:   inventory.MetricTypeCounter,
				Labels: []string{"instance", "job"},
			},
			want: want{typ: inventory.MetricTypeCounter, family: "grpc"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.MetricInventory{Metrics: append([]inventory.MetricDescriptor{tc.descriptor}, tc.extra...)}
			inv.Sort()
			got := Classify(inv)
			// Find the primary descriptor in the output.
			var cm *ClassifiedMetric
			for i := range got.Metrics {
				if got.Metrics[i].Descriptor.Name == tc.descriptor.Name {
					cm = &got.Metrics[i]
					break
				}
			}
			if cm == nil {
				t.Fatalf("classified metric %q not found", tc.descriptor.Name)
			}
			if cm.Type != tc.want.typ {
				t.Errorf("type = %q, want %q", cm.Type, tc.want.typ)
			}
			if cm.Family != tc.want.family {
				t.Errorf("family = %q, want %q", cm.Family, tc.want.family)
			}
			if cm.Unit != tc.want.unit {
				t.Errorf("unit = %q, want %q", cm.Unit, tc.want.unit)
			}
			for _, wantTrait := range tc.want.traits {
				if !cm.HasTrait(wantTrait) {
					t.Errorf("missing trait %q", wantTrait)
				}
			}
			// Assert no unexpected traits in the positive direction.
			if len(tc.want.traits) == 0 && len(cm.Traits) != 0 {
				t.Errorf("unexpected traits: %v", cm.Traits)
			}
		})
	}
}

func TestClassify_NilSafe(t *testing.T) {
	got := Classify(nil)
	if got == nil || got.Inventory == nil {
		t.Fatalf("Classify(nil) should return non-nil inventory wrapper")
	}
	if len(got.Metrics) != 0 {
		t.Fatalf("Classify(nil) should have zero metrics, got %d", len(got.Metrics))
	}
}

func TestClassify_DeterministicOrdering(t *testing.T) {
	inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{
		{Name: "b_total"},
		{Name: "a_total"},
		{Name: "c_total"},
	}}
	inv.Sort()
	got := Classify(inv)
	want := []string{"a_total", "b_total", "c_total"}
	if len(got.Metrics) != len(want) {
		t.Fatalf("metric count = %d, want %d", len(got.Metrics), len(want))
	}
	for i, name := range want {
		if got.Metrics[i].Descriptor.Name != name {
			t.Errorf("metric[%d] = %q, want %q", i, got.Metrics[i].Descriptor.Name, name)
		}
	}
}

// TestClassify_HelpTextHints exercises the positive path where labels alone
// are ambiguous but help text strongly implies a service shape. Per Step 2.0,
// the help-text hint must ADD the trait when no contradicting label/name
// signal is present.
func TestClassify_HelpTextHints(t *testing.T) {
	tests := []struct {
		name       string
		descriptor inventory.MetricDescriptor
		wantTrait  Trait
	}{
		{
			// Labels are ambiguous (instance/job only). Help text says
			// "outbound HTTP client request" — a strict-pattern match
			// that triggers the HTTP hint.
			name: "ambiguous_labels_help_says_http_client_request",
			descriptor: inventory.MetricDescriptor{
				Name:   "outbound_calls_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Duration of outbound HTTP client request in seconds",
				Labels: []string{"instance", "job"},
			},
			wantTrait: TraitServiceHTTP,
		},
		{
			// gRPC hint via "gRPC call" phrasing. Labels do not include
			// any grpc_* keys, so the hint is the only path to the trait.
			name: "ambiguous_labels_help_says_grpc_call",
			descriptor: inventory.MetricDescriptor{
				Name:   "outbound_invocations_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Total number of gRPC call attempts to upstream services",
				Labels: []string{"instance", "job"},
			},
			wantTrait: TraitServiceGRPC,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{tc.descriptor}}
			inv.Sort()
			got := Classify(inv)
			if len(got.Metrics) != 1 {
				t.Fatalf("classified %d metrics, want 1", len(got.Metrics))
			}
			if !got.Metrics[0].HasTrait(tc.wantTrait) {
				t.Errorf("missing help-text trait %q on %q (traits=%v)",
					tc.wantTrait, tc.descriptor.Name, got.Metrics[0].Traits)
			}
		})
	}
}

// TestClassify_HelpTextHints_LatencyNotPromoted locks in the contract that
// helpHints does NOT promote TraitLatencyHistogram. The latency-histogram
// trait is structural (must be a "_bucket" descriptor with an "le" label).
// Promoting it from help text alone would attach the trait to the
// accompanying _count and _sum series of the same histogram, which causes
// downstream recipes (service_http_latency, service_grpc_latency, etc.)
// to emit spurious panels. Regression for service-basic golden break.
func TestClassify_HelpTextHints_LatencyNotPromoted(t *testing.T) {
	tests := []struct {
		name       string
		descriptor inventory.MetricDescriptor
	}{
		{
			// Counter accompanying a histogram — same help text as the
			// _bucket descriptor would have, but no "le" label and no
			// "_bucket" suffix. Help text alone must not promote latency.
			name: "histogram_count_companion_help_says_duration",
			descriptor: inventory.MetricDescriptor{
				Name:   "http_request_duration_seconds_count",
				Type:   inventory.MetricTypeHistogram,
				Help:   "Duration of HTTP request in seconds",
				Labels: []string{"instance", "job"},
			},
		},
		{
			// Sum accompanying a histogram — same as above.
			name: "histogram_sum_companion_help_says_duration",
			descriptor: inventory.MetricDescriptor{
				Name:   "http_request_duration_seconds_sum",
				Type:   inventory.MetricTypeHistogram,
				Help:   "Duration of HTTP request in seconds",
				Labels: []string{"instance", "job"},
			},
		},
		{
			// Plain histogram with "duration" in help but no _bucket
			// suffix and no le label — structural test fails, so trait
			// must not be present.
			name: "histogram_help_mentions_duration_no_bucket",
			descriptor: inventory.MetricDescriptor{
				Name:   "outbound_calls",
				Type:   inventory.MetricTypeHistogram,
				Help:   "Histogram of outbound call duration",
				Labels: []string{"instance", "job"},
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{tc.descriptor}}
			inv.Sort()
			got := Classify(inv)
			if len(got.Metrics) != 1 {
				t.Fatalf("classified %d metrics, want 1", len(got.Metrics))
			}
			if got.Metrics[0].HasTrait(TraitLatencyHistogram) {
				t.Errorf("help text promoted TraitLatencyHistogram on %q (traits=%v); "+
					"latency trait must be structural-only",
					tc.descriptor.Name, got.Metrics[0].Traits)
			}
		})
	}
}

// TestClassify_HelpTextHints_LabelsWin is the adversarial regression: when
// labels point to one shape and help text points to another, the labels MUST
// win. This is the V0.2-PLAN §7 invariant — help text is unreliable and may
// never override label evidence.
func TestClassify_HelpTextHints_LabelsWin(t *testing.T) {
	tests := []struct {
		name        string
		descriptor  inventory.MetricDescriptor
		mustHave    []Trait
		mustNotHave []Trait
	}{
		{
			// Labels say db_query (no HTTP-shape labels), help text
			// says "http request". Help-text HTTP hint must NOT apply
			// because the labels carry no HTTP signal — but more
			// importantly, the test asserts the label-derived absence
			// of the trait holds even with the misleading help text.
			// The metric has the db_query label but no method/route/
			// status_code/handler/code, so it must remain HTTP-free.
			name: "labels_say_db_query_help_says_http_request_no_http_trait",
			descriptor: inventory.MetricDescriptor{
				Name:   "service_operations_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Total http request count by db_query type",
				Labels: []string{"db_query", "instance", "job"},
			},
			mustNotHave: []Trait{TraitServiceHTTP},
		},
		{
			// Labels carry the gRPC shape; misleading help text talks
			// about "HTTP server". The label-derived TraitServiceGRPC
			// must remain, and the help-text HTTP hint must be
			// suppressed because TraitServiceGRPC is already set.
			name: "labels_say_grpc_help_says_http_server_keeps_grpc_only",
			descriptor: inventory.MetricDescriptor{
				Name:   "rpc_handled_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Counter of HTTP server requests handled by the layer",
				Labels: []string{"grpc_code", "grpc_method", "grpc_service"},
			},
			mustHave:    []Trait{TraitServiceGRPC},
			mustNotHave: []Trait{TraitServiceHTTP},
		},
		{
			// Labels carry the HTTP shape; misleading help text talks
			// about "gRPC call". The label-derived TraitServiceHTTP
			// must remain, and the help-text gRPC hint must be
			// suppressed because TraitServiceHTTP is already set.
			name: "labels_say_http_help_says_grpc_keeps_http_only",
			descriptor: inventory.MetricDescriptor{
				Name:   "service_calls_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Number of gRPC call invocations from this service",
				Labels: []string{"method", "route", "status_code"},
			},
			mustHave:    []Trait{TraitServiceHTTP},
			mustNotHave: []Trait{TraitServiceGRPC},
		},
		{
			// Negation phrasing in help text must not trigger a hint.
			// "no HTTP-shape labels" / "no grpc_method labels" mention
			// http/grpc but do not contain any of the strict trigger
			// nouns ("request", "call", "server", ...). This is the
			// concrete look-alike from the realistic fixtures.
			name: "negation_phrasing_in_help_does_not_trigger",
			descriptor: inventory.MetricDescriptor{
				Name:   "queue_items_received_total",
				Type:   inventory.MetricTypeCounter,
				Help:   "Items the queue worker received; status holds domain values, NOT HTTP status codes; carries no grpc_method labels.",
				Labels: []string{"instance", "job", "status"},
			},
			mustNotHave: []Trait{TraitServiceHTTP, TraitServiceGRPC},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.MetricInventory{Metrics: []inventory.MetricDescriptor{tc.descriptor}}
			inv.Sort()
			got := Classify(inv)
			if len(got.Metrics) != 1 {
				t.Fatalf("classified %d metrics, want 1", len(got.Metrics))
			}
			cm := got.Metrics[0]
			for _, want := range tc.mustHave {
				if !cm.HasTrait(want) {
					t.Errorf("missing required trait %q (traits=%v)", want, cm.Traits)
				}
			}
			for _, banned := range tc.mustNotHave {
				if cm.HasTrait(banned) {
					t.Errorf("help-text override leaked trait %q onto %q (traits=%v)",
						banned, tc.descriptor.Name, cm.Traits)
				}
			}
		})
	}
}

// TestClassify_HelpTextHints_NoAI is a unit-level regression confirming the
// help-hint code path is purely deterministic: no goroutines spawned, no
// network calls, no context usage. We assert the function is pure by:
//
//  1. Calling it many times in a loop and checking the goroutine count is
//     stable end-to-end (i.e. nothing leaked an async worker).
//  2. Verifying repeated calls on identical input return identical output
//     (no side effects, no hidden state).
func TestClassify_HelpTextHints_NoAI(t *testing.T) {
	helpTexts := []string{
		"Duration of outbound HTTP client request in seconds",
		"Histogram of gRPC server call latency in seconds",
		"Total HTTP requests processed by the API",
		"",
		"Cumulative cpu time consumed in seconds.",
		"no grpc_method labels here",
		"NOT HTTP status codes",
	}

	before := runtime.NumGoroutine()
	var first [][]Trait
	for _, h := range helpTexts {
		first = append(first, helpHints(h))
	}
	// Hammer the code path many times. Any background worker would have
	// to be spawned somewhere in this loop; the goroutine count after
	// the loop must equal the count before.
	for i := 0; i < 1000; i++ {
		for _, h := range helpTexts {
			_ = helpHints(h)
		}
	}
	after := runtime.NumGoroutine()
	if after != before {
		t.Errorf("helpHints leaked goroutines: before=%d after=%d", before, after)
	}

	// Determinism / purity: a second pass over the same inputs must
	// produce identical outputs in identical order.
	for i, h := range helpTexts {
		got := helpHints(h)
		if !equalTraitSlices(got, first[i]) {
			t.Errorf("helpHints non-deterministic for help=%q: first=%v second=%v",
				h, first[i], got)
		}
	}
}

// equalTraitSlices is a strict order-sensitive equality used by the
// determinism assertion in TestClassify_HelpTextHints_NoAI.
func equalTraitSlices(a, b []Trait) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
