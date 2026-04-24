package classify

import (
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
