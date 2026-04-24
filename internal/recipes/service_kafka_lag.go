// Package recipes – service_kafka_consumer_lag recipe.
//
// # Operator question
//
// Is any Kafka consumer group falling behind? Consumer lag is the number of
// messages between the consumer's current offset and the partition's latest
// offset. A non-zero and growing lag means the consumer cannot keep up with
// the producer and will eventually cause latency spikes or alert fatigue
// downstream.
//
// # Promotion note
//
// This recipe was promoted from Tier-3 (RECIPES.md §5) to first-class status
// because consumer lag is the #1 operator metric in any Kafka-consuming fleet.
// Tier-3 entries are "ideas worth tracking but not yet vetted"; this one proved
// universally present in service dashboards.
//
// # Canonical signals
//
// Two gauges from kafka_exporter (daniel-nichter/kafka_exporter, the most
// widely deployed Kafka exporter):
//
//	kafka_consumergroup_lag      – per-partition lag gauge, labelled by
//	                               {consumergroup, topic, partition}
//	kafka_consumergroup_lag_sum  – aggregate lag per consumergroup (sum across
//	                               all partitions already done by the exporter)
//
// # Known alternative names (not handled here, reserved for future extension)
//
//   - kafka_consumer_lag_messages                              (JMX exporter shape)
//   - kafka_consumer_fetch_manager_metrics_records_lag_max    (Prometheus native broker export)
//   - kafka_consumer_group_lag                                (Bitnami Kafka chart exporter)
//
// # Aggregation shape
//
// max by (<grouping>) (<metric>)
//
// max rather than sum: different replicas of the same consumer group report
// their own per-partition counts. Summing would double-count when multiple
// replicas happen to monitor the same partition. max picks the worst-case lag,
// which is the operationally relevant number.
//
// # Confidence: 0.85
//
// Metric name equality alone could reach 0.90, but gauges named
// kafka_consumergroup_lag could hypothetically appear in non-exporter
// instrumentation with a different semantic. 0.85 reflects that small
// uncertainty.
//
// # Section: errors
//
// Growing lag is an error signal: the consumer is falling behind and will
// eventually be unable to serve its downstream dependencies on time.
package recipes

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// kafkaLagMetrics is the fixed set of kafka_exporter lag gauges this recipe
// handles, together with human-readable panel titles.
var kafkaLagMetrics = []struct {
	name  string
	title string
}{
	{"kafka_consumergroup_lag", "Kafka consumer lag"},
	{"kafka_consumergroup_lag_sum", "Kafka consumer lag (sum)"},
}

// kafkaLagNames is the same set keyed for O(1) lookup during Match.
var kafkaLagNames = func() map[string]bool {
	m := make(map[string]bool, len(kafkaLagMetrics))
	for _, e := range kafkaLagMetrics {
		m[e.name] = true
	}
	return m
}()

type serviceKafkaConsumerLagRecipe struct{}

// NewServiceKafkaConsumerLag returns the service_kafka_consumer_lag recipe.
func NewServiceKafkaConsumerLag() Recipe { return &serviceKafkaConsumerLagRecipe{} }

func (serviceKafkaConsumerLagRecipe) Name() string    { return "service_kafka_consumer_lag" }
func (serviceKafkaConsumerLagRecipe) Section() string { return "errors" }

func (r serviceKafkaConsumerLagRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge {
		return false
	}
	return kafkaLagNames[m.Descriptor.Name]
}

func (r serviceKafkaConsumerLagRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}

	// Collect which of the lag gauges are actually present in the inventory.
	present := make(map[string]bool, len(kafkaLagMetrics))
	for _, m := range inv.Metrics {
		if kafkaLagNames[m.Descriptor.Name] {
			present[m.Descriptor.Name] = true
		}
	}
	if len(present) == 0 {
		return nil
	}

	// Build a lookup from metric name to its classified view so we can pass it
	// to safeGroupLabels.
	byName := make(map[string]ClassifiedMetricView, len(inv.Metrics))
	for _, m := range inv.Metrics {
		if kafkaLagNames[m.Descriptor.Name] {
			byName[m.Descriptor.Name] = m
		}
	}

	var panels []ir.Panel
	for _, e := range kafkaLagMetrics {
		if !present[e.name] {
			continue
		}
		panels = append(panels, r.lagPanel(e.title, e.name, byName[e.name]))
	}

	// Stable intra-recipe ordering: sort panels by title.
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}

func (r serviceKafkaConsumerLagRecipe) lagPanel(title, metric string, m ClassifiedMetricView) ir.Panel {
	group := safeGroupLabels(m, "consumergroup", "topic", "partition")
	expr := fmt.Sprintf(
		`max by (%s) (%s)`,
		strings.Join(group, ", "), metric,
	)
	return ir.Panel{
		Title: title,
		Kind:  ir.PanelKindTimeSeries,
		Unit:  "short",
		Queries: []ir.QueryCandidate{{
			Expr:         expr,
			LegendFormat: legendFor(group),
			Unit:         "short",
		}},
		Confidence: 0.85,
		Rationale: fmt.Sprintf(
			"kafka_exporter gauge %q; max by consumergroup/topic/partition avoids double-counting when multiple replicas report the same partition.",
			metric,
		),
	}
}
