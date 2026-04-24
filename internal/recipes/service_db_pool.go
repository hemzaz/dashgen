package recipes

// service_db_pool — DB connection pool saturation ratio
//
// Operator question: is the application exhausting its database connection pool?
//
// Signals (two canonical families):
//
//   stdlib sql.DB (prometheus/client_golang):
//     - go_sql_stats_connections_in_use  (gauge): connections currently checked out
//     - go_sql_stats_connections_max     (gauge): pool ceiling (sql.DB.SetMaxOpenConns)
//
//   pgxpool (jackc/pgx):
//     - pgxpool_acquired_connections     (gauge): connections currently acquired
//     - pgxpool_max_connections          (gauge): pool ceiling (pgxpool.Config.MaxConns)
//
// Aggregation: in_use / max, no grouping rewrite — both sides share identical
// label sets ({instance, job} and optionally db_name), so plain division aligns
// correctly. The ratio tells the operator how close the pool is to saturation;
// values above 0.80 warrant investigation.
//
// Confidence: 0.80 — both metric names are canonical strings from their
// respective Prometheus client implementations. The pair-check removes the risk
// of emitting a broken expression when only one half is present.
//
// Section choice: saturation.
// Pool exhaustion is a classic saturation signal: the resource (connections)
// is finite and the current demand is approaching the ceiling.
//
// Profile gate: ProfileService only.
// DB pool metrics originate from application-level instrumentation; they are
// not present on infra or Kubernetes targets.
//
// Match rule: fires on the in-use/acquired gauge (the "current usage" side).
// BuildPanels verifies the corresponding max gauge is also in the snapshot and
// emits 0 panels if it is absent.

import (
	"fmt"
	"sort"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// dbPoolPair describes one in-use/max gauge pair.
type dbPoolPair struct {
	inUseMetric string
	maxMetric   string
	titlePrefix string
}

var dbPoolPairs = []dbPoolPair{
	{
		inUseMetric: "go_sql_stats_connections_in_use",
		maxMetric:   "go_sql_stats_connections_max",
		titlePrefix: "go_sql",
	},
	{
		inUseMetric: "pgxpool_acquired_connections",
		maxMetric:   "pgxpool_max_connections",
		titlePrefix: "pgxpool",
	},
}

type serviceDBPoolRecipe struct{}

// NewServiceDBPool returns the service_db_pool recipe.
func NewServiceDBPool() Recipe { return &serviceDBPoolRecipe{} }

func (serviceDBPoolRecipe) Name() string    { return "service_db_pool" }
func (serviceDBPoolRecipe) Section() string { return "saturation" }

// Match fires on the in-use/acquired gauge side of any recognised pair.
// The max gauge alone does not trigger the recipe; BuildPanels handles
// the pair-presence check.
func (r serviceDBPoolRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeGauge && m.Type != inventory.MetricTypeUnknown {
		return false
	}
	for _, pair := range dbPoolPairs {
		if m.Descriptor.Name == pair.inUseMetric {
			return true
		}
	}
	return false
}

func (r serviceDBPoolRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}

	// Index the metric names present in the snapshot.
	present := make(map[string]bool, len(inv.Metrics))
	for _, m := range inv.Metrics {
		present[m.Descriptor.Name] = true
	}

	var panels []ir.Panel
	for _, pair := range dbPoolPairs {
		if !present[pair.inUseMetric] || !present[pair.maxMetric] {
			continue
		}
		expr := fmt.Sprintf("%s / %s", pair.inUseMetric, pair.maxMetric)
		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("DB pool utilization: %s", pair.titlePrefix),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "percentunit",
			Queries: []ir.QueryCandidate{{
				Expr:         expr,
				LegendFormat: "{{instance}} {{job}}",
				Unit:         "percentunit",
			}},
			Confidence: 0.80,
			Rationale: fmt.Sprintf(
				"%s / %s: connection pool utilisation ratio. "+
					"Values near 1.0 indicate pool exhaustion.",
				pair.inUseMetric, pair.maxMetric,
			),
		})
	}

	// Deterministic order by title.
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].Title < panels[j].Title
	})
	return panels
}
