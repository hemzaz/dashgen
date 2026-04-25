// Package synth transforms a classified inventory into a dashboard IR by
// applying recipes for a profile.
//
// Determinism contract:
//   - Sections iterate in profiles.Sections(profile) order.
//   - Within a section, recipes run in registry (Name-sorted) order.
//   - Panels emitted by each recipe retain that recipe's own intra-call
//     order; BuildPanels implementations must sort their output.
//   - Panel UIDs are assigned here so dashboard UID is known first.
//   - If the aggregated panel count exceeds profiles.PanelCap(profile),
//     panels are kept by highest Confidence desc, then by UID asc
//     (alphabetical) as an explicit tie-break.
package synth

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"dashgen/internal/classify"
	"dashgen/internal/ids"
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/recipes"
)

// metricNamePattern matches the first Prometheus-style identifier in an
// expression; used to pull a metric name back out of a QueryCandidate expr
// for stable panel UID material.
var metricNamePattern = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)

// Synthesize builds an IR dashboard from a classified inventory using the
// profile's default panel cap.
func Synthesize(inv *classify.ClassifiedInventory, profile profiles.Profile, reg *recipes.Registry) *ir.Dashboard {
	return SynthesizeWithCap(inv, profile, reg, 0)
}

// SynthesizeWithCap is Synthesize with an explicit panel-cap override. A
// non-positive cap falls back to profiles.PanelCap(profile).
func SynthesizeWithCap(inv *classify.ClassifiedInventory, profile profiles.Profile, reg *recipes.Registry, capOverride int) *ir.Dashboard {
	var rawInv *inventory.MetricInventory
	if inv != nil {
		rawInv = inv.Inventory
	}
	invHash := inventory.InventoryHash(rawInv)
	uid := ids.DashboardUID(string(profile), invHash)

	snapshot := snapshotOf(inv)

	dashboard := &ir.Dashboard{
		UID:     uid,
		Title:   defaultTitle(profile, invHash),
		Profile: string(profile),
		Variables: []ir.Variable{
			{Name: "datasource", Label: "Data source", Query: "prometheus"},
		},
	}
	if reg == nil {
		return dashboard
	}

	// Walk sections in the canonical profile order; for each section, pull
	// recipes that declare Section() == name, and emit their panels in
	// registry order.
	var rows []ir.Row
	for _, section := range profiles.Sections(profile) {
		var panels []ir.Panel
		for _, rec := range reg.All() {
			if rec.Section() != section {
				continue
			}
			built := rec.BuildPanels(snapshot, profile)
			for i := range built {
				panels = append(panels, assignPanelUID(built[i], uid, section, rec.Name()))
			}
		}
		if len(panels) == 0 {
			// SPECS Rule 5: omit weak/empty sections.
			continue
		}
		rows = append(rows, ir.Row{Title: section, Panels: panels})
	}

	// Enforce the panel cap. Rank panels across the whole dashboard by
	// (Confidence desc, UID asc) and drop the tail. After the cull, keep
	// the rows in the original section order.
	cap := capOverride
	if cap <= 0 {
		cap = profiles.PanelCap(profile)
	}
	totalPanels := 0
	for _, r := range rows {
		totalPanels += len(r.Panels)
	}
	if totalPanels > cap {
		rows = enforcePanelCap(rows, cap)
	}

	dashboard.Rows = rows
	return dashboard
}

// snapshotOf converts the classify output into the recipe-facing snapshot.
// The function intentionally copies traits to []string so recipes do not
// need to import internal/classify.
func snapshotOf(inv *classify.ClassifiedInventory) recipes.ClassifiedInventorySnapshot {
	if inv == nil {
		return recipes.ClassifiedInventorySnapshot{Inventory: &inventory.MetricInventory{}}
	}
	views := make([]recipes.ClassifiedMetricView, 0, len(inv.Metrics))
	for _, cm := range inv.Metrics {
		traits := make([]string, 0, len(cm.Traits))
		for _, t := range cm.Traits {
			traits = append(traits, string(t))
		}
		unit := cm.Unit
		if unit == "" {
			unit = cm.Descriptor.InferredUnit
		}
		views = append(views, recipes.ClassifiedMetricView{
			Descriptor: cm.Descriptor,
			Type:       cm.Type,
			Family:     cm.Family,
			Unit:       unit,
			Traits:     traits,
		})
	}
	return recipes.ClassifiedInventorySnapshot{
		Inventory: inv.Inventory,
		Metrics:   views,
	}
}

// assignPanelUID fills in a stable UID for a panel using
// (dashboardUID, section, recipeName/metricName, kind). The recipe
// name is composed into the metric-name slot so two distinct recipes
// matching the same metric in the same section (e.g. service_http_rate
// and service_client_http both keying off http_client_requests_total
// in the "traffic" section) produce distinct UIDs. metricName is
// pulled from the first query's expression; if extraction fails, the
// recipe name alone is used.
func assignPanelUID(p ir.Panel, dashboardUID, section, recipeName string) ir.Panel {
	metricName := ""
	if len(p.Queries) > 0 {
		metricName = firstIdentifier(p.Queries[0].Expr)
	}
	material := recipeName
	if metricName != "" {
		material = recipeName + "/" + metricName
	}
	p.UID = ids.PanelUID(dashboardUID, section, material, string(p.Kind))
	return p
}

// firstIdentifier extracts the first PromQL-valid metric identifier
// from an expression so it can become panel-UID material. Two-pass
// strategy:
//
//  1. If the expression contains a counter-rate wrapper (rate(),
//     irate(), increase(), delta()), the inner argument's leading
//     identifier IS the metric — return it. This catches the recipe
//     pattern `sum by (...) (rate(<metric>[5m]))` and the histogram
//     pattern `histogram_quantile(0.95, sum by (le) (rate(<metric>_bucket[5m])))`
//     without being fooled by the by-clause label names.
//  2. Otherwise return the first token that is NOT a known PromQL
//     function/keyword AND contains at least one underscore. The
//     underscore filter is the load-bearing heuristic against
//     by-clause labels (instance, job, le, namespace, pod, mode,
//     handler, code, ...) and Prometheus's own short labels — every
//     metric a dashgen recipe emits contains at least one underscore
//     (http_requests_total, node_cpu_seconds_total, kube_pod_status_phase,
//     etc.). The historical short skip-list (sum/rate/by/...) was not
//     enough on its own and is retained as belt-and-braces.
//
// Returns "" if no identifier matches; the caller falls back to the
// recipe name so panel UIDs never collapse to identical material.
func firstIdentifier(expr string) string {
	if m := identifierInsideRate(expr); m != "" {
		return m
	}
	tokens := metricNamePattern.FindAllString(expr, -1)
	skip := map[string]bool{
		"sum": true, "rate": true, "irate": true, "increase": true,
		"delta": true, "histogram_quantile": true,
		"by": true, "on": true, "ignoring": true, "without": true,
		"avg": true, "max": true, "min": true, "count": true,
		"stddev": true, "stdvar": true, "topk": true, "bottomk": true,
		"quantile": true, "le": true,
	}
	for _, t := range tokens {
		if skip[t] {
			continue
		}
		if !strings.Contains(t, "_") {
			continue
		}
		return t
	}
	return ""
}

// identifierInsideRate scans expr for the first occurrence of a
// counter-rate wrapper and returns the leading identifier of its
// argument. Returns "" if no rate-style wrapper is found.
func identifierInsideRate(expr string) string {
	for _, fn := range []string{"rate(", "irate(", "increase(", "delta("} {
		idx := strings.Index(expr, fn)
		if idx < 0 {
			continue
		}
		inner := expr[idx+len(fn):]
		m := metricNamePattern.FindString(inner)
		if m != "" {
			return m
		}
	}
	return ""
}

// enforcePanelCap keeps the top-N panels by (Confidence desc, UID asc)
// across the whole dashboard, then reassembles rows preserving the original
// section order.
func enforcePanelCap(rows []ir.Row, cap int) []ir.Row {
	type indexed struct {
		rowIdx int
		panel  ir.Panel
	}
	all := make([]indexed, 0)
	for ri, r := range rows {
		for _, p := range r.Panels {
			all = append(all, indexed{rowIdx: ri, panel: p})
		}
	}
	// Sort: higher confidence first; ties broken by UID asc (explicit).
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].panel.Confidence != all[j].panel.Confidence {
			return all[i].panel.Confidence > all[j].panel.Confidence
		}
		return all[i].panel.UID < all[j].panel.UID
	})
	if cap < len(all) {
		all = all[:cap]
	}
	// Rebuild rows in original order.
	kept := make([][]ir.Panel, len(rows))
	for _, idx := range all {
		kept[idx.rowIdx] = append(kept[idx.rowIdx], idx.panel)
	}
	out := make([]ir.Row, 0, len(rows))
	for ri, r := range rows {
		if len(kept[ri]) == 0 {
			continue
		}
		// Restore intra-row original order by re-sorting per UID since
		// panels in a single row shared an input sort, and UID order is
		// stable.
		sort.SliceStable(kept[ri], func(i, j int) bool {
			return kept[ri][i].UID < kept[ri][j].UID
		})
		out = append(out, ir.Row{Title: r.Title, Panels: kept[ri]})
	}
	return out
}

// defaultTitle embeds the first 8 chars of invHash so operators get a visual
// cue when the underlying inventory changes.
func defaultTitle(p profiles.Profile, invHash string) string {
	short := invHash
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("DashGen — %s (%s)", string(p), short)
}
