package recipes

// serviceCacheHitsRecipe answers the question: "What is the cache hit/miss rate
// and the hit ratio for a service's cache layer?"
//
// Canonical signals:
//   - Counter pairs of the form <prefix>_cache_hits_total and
//     <prefix>_cache_misses_total, where <prefix> is any non-empty string.
//     Examples: http_cache_hits_total / http_cache_misses_total,
//               redis_cache_hits_total / redis_cache_misses_total.
//
// Match strategy:
//   Match returns true only for counters whose name ends with _cache_hits_total.
//   BuildPanels requires the corresponding <prefix>_cache_misses_total to be
//   present in the inventory; without the pair no panel is emitted.
//
// Aggregation shape:
//   sum by (instance, job) — one series per (instance, job) tuple.
//   Three query candidates per panel:
//     1. hit rate  (cps)
//     2. miss rate (cps)
//     3. hit ratio (percentunit) = hits / (hits + misses)
//
// Confidence: 0.80 — strong name-suffix match; pairs are commonly co-exported
// but the miss counter could be absent in unusual deployments, so not 0.90.
//
// Known look-alikes that must NOT match:
//   - Metrics ending in _cache_misses_total (mismatch by design; Match is keyed
//     on the hits side only).
//   - Generic counters that happen to contain "cache" but do not follow the
//     <prefix>_cache_{hits,misses}_total naming convention.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const cacheSuffix = "_cache_hits_total"
const cacheMissesSuffix = "_cache_misses_total"

type serviceCacheHitsRecipe struct{}

// NewServiceCacheHits returns the service_cache_hits recipe.
func NewServiceCacheHits() Recipe { return &serviceCacheHitsRecipe{} }

func (serviceCacheHitsRecipe) Name() string    { return "service_cache_hits" }
func (serviceCacheHitsRecipe) Section() string { return "traffic" }

// Match returns true for counters whose name ends with _cache_hits_total.
func (r serviceCacheHitsRecipe) Match(m ClassifiedMetricView) bool {
	return strings.HasSuffix(m.Descriptor.Name, cacheSuffix)
}

// BuildPanels emits one panel per matched hit/miss pair found in the inventory.
// It returns nil when the profile is not ProfileService or when no complete
// pair is found.
func (r serviceCacheHitsRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}

	// Collect the set of miss-metric names present in the inventory.
	missSet := map[string]bool{}
	for _, m := range inv.Metrics {
		if strings.HasSuffix(m.Descriptor.Name, cacheMissesSuffix) {
			missSet[m.Descriptor.Name] = true
		}
	}

	// For each hits metric, derive the expected misses name and check the pair.
	type pair struct {
		prefix string
		hits   string
		misses string
	}
	seen := map[string]bool{}
	var pairs []pair
	for _, m := range inv.Metrics {
		name := m.Descriptor.Name
		if !strings.HasSuffix(name, cacheSuffix) {
			continue
		}
		prefix := strings.TrimSuffix(name, cacheSuffix)
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		missName := prefix + cacheMissesSuffix
		if !missSet[missName] {
			continue
		}
		pairs = append(pairs, pair{prefix: prefix, hits: name, misses: missName})
	}
	if len(pairs) == 0 {
		return nil
	}

	// Sort pairs by prefix for deterministic panel ordering.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].prefix < pairs[j].prefix
	})

	panels := make([]ir.Panel, 0, len(pairs))
	for _, pr := range pairs {
		hitRate := fmt.Sprintf(
			"sum by (instance, job) (rate(%s[%s]))",
			pr.hits, defaultRateWindow,
		)
		missRate := fmt.Sprintf(
			"sum by (instance, job) (rate(%s[%s]))",
			pr.misses, defaultRateWindow,
		)
		hitRatio := fmt.Sprintf(
			"sum by (instance, job) (rate(%s[%s])) / (sum by (instance, job) (rate(%s[%s])) + sum by (instance, job) (rate(%s[%s])))",
			pr.hits, defaultRateWindow,
			pr.hits, defaultRateWindow,
			pr.misses, defaultRateWindow,
		)

		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Cache hit/miss: %s", pr.prefix),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "cps",
			Queries: []ir.QueryCandidate{
				{
					Expr:         hitRate,
					LegendFormat: "{{instance}} {{job}} hits",
					Unit:         "cps",
				},
				{
					Expr:         missRate,
					LegendFormat: "{{instance}} {{job}} misses",
					Unit:         "cps",
				},
				{
					Expr:         hitRatio,
					LegendFormat: "{{instance}} {{job}} hit ratio",
					Unit:         "percentunit",
				},
			},
			Confidence: 0.80,
			Rationale: fmt.Sprintf(
				"cache hit/miss pair %s / %s: rates per second and hit ratio per (instance, job).",
				pr.hits, pr.misses,
			),
		})
	}
	return panels
}
