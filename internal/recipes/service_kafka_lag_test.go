package recipes

import (
	"testing"

	"dashgen/internal/inventory"
	"dashgen/internal/profiles"
)

func TestServiceKafkaConsumerLag_Match(t *testing.T) {
	r := NewServiceKafkaConsumerLag()

	// Positive cases: both canonical gauge names must match.
	positives := []string{
		"kafka_consumergroup_lag",
		"kafka_consumergroup_lag_sum",
	}
	for _, name := range positives {
		m := ClassifiedMetricView{
			Descriptor: inventory.MetricDescriptor{Name: name},
			Type:       inventory.MetricTypeGauge,
		}
		if !r.Match(m) {
			t.Errorf("expected match on gauge %s", name)
		}
	}

	// Negative: unrelated metric with a similar-sounding name must not match.
	unrelated := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kafka_topic_partition_current_offset"},
		Type:       inventory.MetricTypeGauge,
	}
	if r.Match(unrelated) {
		t.Error("unexpected match on kafka_topic_partition_current_offset")
	}

	// Negative: counter type with a canonical name must not match (recipe requires gauge).
	counter := ClassifiedMetricView{
		Descriptor: inventory.MetricDescriptor{Name: "kafka_consumergroup_lag"},
		Type:       inventory.MetricTypeCounter,
	}
	if r.Match(counter) {
		t.Error("expected no match when type is counter, not gauge")
	}
}

func TestServiceKafkaConsumerLag_BuildPanels(t *testing.T) {
	r := NewServiceKafkaConsumerLag()

	t.Run("both gauges produce two panels in title order", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kafka_consumergroup_lag",
						Labels: []string{"consumergroup", "topic", "partition"},
					},
					Type: inventory.MetricTypeGauge,
				},
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kafka_consumergroup_lag_sum",
						Labels: []string{"consumergroup"},
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 2 {
			t.Fatalf("expected 2 panels, got %d", len(panels))
		}
		// Deterministic title order: "Kafka consumer lag" < "Kafka consumer lag (sum)"
		if panels[0].Title != "Kafka consumer lag" {
			t.Errorf("expected first panel %q, got %q", "Kafka consumer lag", panels[0].Title)
		}
		if panels[1].Title != "Kafka consumer lag (sum)" {
			t.Errorf("expected second panel %q, got %q", "Kafka consumer lag (sum)", panels[1].Title)
		}
		for _, p := range panels {
			if p.Unit != "short" {
				t.Errorf("panel %q: expected unit short, got %q", p.Title, p.Unit)
			}
			if len(p.Queries) != 1 {
				t.Errorf("panel %q: expected 1 query candidate, got %d", p.Title, len(p.Queries))
			}
			if p.Confidence != 0.85 {
				t.Errorf("panel %q: expected confidence 0.85, got %f", p.Title, p.Confidence)
			}
		}
	})

	t.Run("only one gauge produces one panel", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kafka_consumergroup_lag_sum",
						Labels: []string{"consumergroup"},
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileService)
		if len(panels) != 1 {
			t.Fatalf("expected 1 panel, got %d", len(panels))
		}
		if panels[0].Title != "Kafka consumer lag (sum)" {
			t.Errorf("expected panel title %q, got %q", "Kafka consumer lag (sum)", panels[0].Title)
		}
	})

	t.Run("non-service profile returns nil", func(t *testing.T) {
		inv := ClassifiedInventorySnapshot{
			Metrics: []ClassifiedMetricView{
				{
					Descriptor: inventory.MetricDescriptor{
						Name:   "kafka_consumergroup_lag",
						Labels: []string{"consumergroup", "topic", "partition"},
					},
					Type: inventory.MetricTypeGauge,
				},
			},
		}
		panels := r.BuildPanels(inv, profiles.ProfileInfra)
		if panels != nil {
			t.Errorf("expected nil for non-service profile, got %v", panels)
		}
	})
}
