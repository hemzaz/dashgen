// yaml_recipe.go — Recipe interface implementation for v0.3 DSL recipes (T1A.5).
//
// YAMLRecipe wraps a single LoadedRecipe (from loader.go) and exposes the
// existing recipes.Recipe interface (types.go) so synth/validate/render
// stay agnostic to the authoring format.
//
// Composition (DSL §3 lifecycle):
//
//	loader.Load → LoadedRecipe
//	               │
//	               ▼
//	NewYAMLRecipe ─→ ValidateBudget(matcher)        — pre-warm regex cache
//	               ─→ Parse(panel.title_template)    — pre-parse text/template
//	               ─→ Parse(panel.query_template)
//	               ─→ Parse(panel.legend_template)
//
//	YAMLRecipe.Match(m)        ─→ matcher.Eval
//	YAMLRecipe.BuildPanels(s)  ─→ pair.Resolve (if PairWith)
//	                           ─→ Template.Render per panel × per quantile
//	                           ─→ ir.Panel
//
// Determinism: snapshot.Metrics is iterated in slice order (pre-sorted by
// inventory). PanelTemplate slice is iterated in YAML source order.
// Quantile slice in YAML source order. No map iteration on the hot path.
package recipes

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// YAMLRecipe implements Recipe from a parsed LoadedRecipe. Templates and
// the matcher's regex cache are pre-warmed at construction so Match and
// BuildPanels never compile on the hot path.
type YAMLRecipe struct {
	Spec   RecipeSpec
	Source string // SourceBuiltin | SourceUser
	Path   string

	// Pre-parsed templates per panel index. Lengths always match
	// len(Spec.Panels) post-construction.
	titleTmpls  []*Template
	queryTmpls  []*Template
	legendTmpls []*Template
}

// NewYAMLRecipe constructs a YAMLRecipe from a LoadedRecipe. It:
//
//   - Validates the predicate budget (depth, node count, regex compile)
//     via ValidateBudget — populates the package-level regex cache as a
//     side effect (matcher.go).
//   - Parses every panel's title/query/legend templates via template.Parse
//     so render time has zero parse cost.
//
// Returns a non-nil error if any template fails to parse or if the
// predicate budget exceeds limits. The error wraps the failure with the
// recipe name and source path so logs identify the offending file.
func NewYAMLRecipe(loaded LoadedRecipe) (*YAMLRecipe, error) {
	name := loaded.Spec.Metadata.Name

	if err := ValidateBudget(loaded.Spec.Match); err != nil {
		return nil, fmt.Errorf("recipe %s (%s): %w", name, loaded.Path, err)
	}

	y := &YAMLRecipe{
		Spec:   loaded.Spec,
		Source: loaded.Source,
		Path:   loaded.Path,
	}

	for i, panel := range loaded.Spec.Panels {
		base := fmt.Sprintf("%s.panels[%d]", name, i)

		title, err := Parse(base+".title", panel.TitleTemplate)
		if err != nil {
			return nil, fmt.Errorf("recipe %s (%s): %w", name, loaded.Path, err)
		}
		y.titleTmpls = append(y.titleTmpls, title)

		query, err := Parse(base+".query", panel.QueryTemplate)
		if err != nil {
			return nil, fmt.Errorf("recipe %s (%s): %w", name, loaded.Path, err)
		}
		y.queryTmpls = append(y.queryTmpls, query)

		legend, err := Parse(base+".legend", panel.LegendTemplate)
		if err != nil {
			return nil, fmt.Errorf("recipe %s (%s): %w", name, loaded.Path, err)
		}
		y.legendTmpls = append(y.legendTmpls, legend)
	}

	return y, nil
}

// Recipe interface impls --------------------------------------------------

// Name returns the recipe's stable identifier.
func (y *YAMLRecipe) Name() string { return y.Spec.Metadata.Name }

// Section returns the dashboard section this recipe contributes to.
func (y *YAMLRecipe) Section() string { return y.Spec.Metadata.Section }

// Match delegates to the package matcher.
func (y *YAMLRecipe) Match(m ClassifiedMetricView) bool {
	return Eval(y.Spec.Match, m)
}

// BuildPanels is the integration point: matcher + pair resolver +
// template engine.
//
// For each metric in the inventory snapshot:
//  1. Skip if Match is false.
//  2. If PairWith is declared, resolve via pair.Resolve. Honor on_missing.
//  3. For each PanelTemplate in y.Spec.Panels:
//     - Skip if RequiresMetricType is set and disagrees with metric.Type.
//     - Skip if RequiresPair and pair resolution failed under "omit".
//     - Compute GroupBy via safeGroupLabels(PreferredLabels...).
//     - For each quantile in panel.Quantiles (or once if empty),
//     render title/query/legend and emit one ir.Panel.
//
// The returned panels carry UID="" — synth fills the UID after computing
// the dashboard UID (preserves the v0.1/v0.2 contract).
func (y *YAMLRecipe) BuildPanels(snapshot ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	// T17: profile binding. A recipe never fires outside its declared profile.
	if string(p) != y.Spec.Metadata.Profile {
		return nil
	}

	out := make([]ir.Panel, 0, len(snapshot.Metrics))

	for _, m := range snapshot.Metrics {
		if !y.Match(m) {
			continue
		}

		// --- Pair resolution -------------------------------------------------
		var pair *PairContext
		skipMetric := false
		if y.Spec.PairWith != nil {
			res, err := Resolve(*y.Spec.PairWith, snapshot, m)
			switch {
			case err == nil:
				pair = res

			case errors.Is(err, ErrNoCandidate):
				// Matched metric doesn't satisfy the pair spec's precondition
				// (e.g. lacks the expected suffix). Skip this metric — the
				// recipe is misconfigured for this match, but other metrics
				// in the snapshot may still be valid.
				continue

			default:
				// Missing-pair: dispatch on the on_missing policy.
				var pme *PairMissingError
				if errors.As(err, &pme) {
					switch pme.OnMissing {
					case OmitOnMissing:
						skipMetric = true // recipe does not fire for this metric
					case WarnOnMissing:
						// Pair-dependent panels skip via RequiresPair check
						// below; non-pair panels still emit. pair stays nil.
					case UseFirstOnly:
						// Degrade: pair stays nil; pair-dependent panels skip
						// and non-pair panels emit. (Phase 1A keeps this path
						// minimal — degradation behavior is a recipe-author
						// concern via panel.RequiresPair.)
					}
				} else {
					// Unknown error class — be conservative and skip.
					skipMetric = true
				}
			}
		}
		if skipMetric {
			continue
		}

		// --- Per-panel render ------------------------------------------------
		for i, panel := range y.Spec.Panels {
			// Type-dispatch (e.g. service_gc_pause's summary vs histogram split).
			if panel.RequiresMetricType != "" && string(m.Type) != panel.RequiresMetricType {
				continue
			}
			if panel.RequiresPair && pair == nil {
				continue
			}

			group := computeGroupBy(panel, m)
			window := panel.RateWindow
			if window == "" {
				window = defaultRateWindow
			}
			labelMap := labelNamesMap(m.Descriptor.Labels)
			labelList := append([]string(nil), m.Descriptor.Labels...)
			sort.Strings(labelList)

			emit := func(ctx RenderContext) {
				title, err := y.titleTmpls[i].Render(ctx)
				if err != nil {
					return
				}
				expr, err := y.queryTmpls[i].Render(ctx)
				if err != nil {
					return
				}
				legend, err := y.legendTmpls[i].Render(ctx)
				if err != nil {
					return
				}
				out = append(out, ir.Panel{
					UID:        "", // set by synth after dashboardUID computed
					Title:      strings.TrimSpace(title),
					Kind:       panelKindFor(panel.Kind),
					Unit:       panel.Unit,
					Confidence: y.Spec.Metadata.Confidence,
					Queries: []ir.QueryCandidate{{
						Expr:         strings.TrimSpace(expr),
						LegendFormat: strings.TrimSpace(legend),
						Unit:         panel.Unit,
					}},
					Rationale: y.rationale(m, panel, group, pair),
				})
			}

			baseCtx := RenderContext{
				Metric:          m.Descriptor.Name,
				Type:            string(m.Type),
				Labels:          labelMap,
				LabelList:       labelList,
				ScopeFilter:     "", // synth supplies via $vars at render time
				Window:          window,
				GroupBy:         group,
				PreferredLabels: panel.PreferredLabels,
				Pair:            pair,
			}

			if len(panel.Quantiles) == 0 {
				emit(baseCtx)
				continue
			}
			for _, q := range panel.Quantiles {
				ctx := baseCtx
				ctx.Quantile = formatQuantile(q)
				ctx.Quantile100 = formatQuantile100(q)
				emit(ctx)
			}
		}
	}

	return out
}

// rationale composes a short human-readable trace string for the panel.
// Mirrors the existing Go recipes' rationale format so warnings.json /
// rationale.md stay consistent.
func (y *YAMLRecipe) rationale(m ClassifiedMetricView, panel PanelTemplate, group []string, pair *PairContext) string {
	_ = panel // panel index/title is in the panel itself; kept for future telemetry
	pairBit := ""
	if pair != nil {
		pairBit = fmt.Sprintf(" paired with %q.", pair.Name)
	}
	return fmt.Sprintf(
		"YAML recipe %s (%s); metric %q (%s) grouped by %s.%s",
		y.Spec.Metadata.Name,
		y.Spec.Metadata.Tier,
		m.Descriptor.Name,
		m.Type,
		strings.Join(group, ", "),
		pairBit,
	)
}

// computeGroupBy returns the group_by labels for this panel and metric:
// either panel.GroupBy verbatim (filtered for banned labels) or
// safeGroupLabels with the panel's preferred labels.
func computeGroupBy(panel PanelTemplate, m ClassifiedMetricView) []string {
	if len(panel.GroupBy) > 0 {
		out := make([]string, 0, len(panel.GroupBy))
		seen := map[string]bool{}
		for _, l := range panel.GroupBy {
			if bannedLabels[l] || seen[l] {
				continue
			}
			seen[l] = true
			out = append(out, l)
		}
		return out
	}
	return safeGroupLabels(m, panel.PreferredLabels...)
}

// labelNamesMap produces the names-only map carried by RenderContext.
// Values are always "" (invariant I2 — never leak label values into
// templates).
func labelNamesMap(labels []string) map[string]string {
	out := make(map[string]string, len(labels))
	for _, l := range labels {
		out[l] = ""
	}
	return out
}

// formatQuantile returns the canonical string form of a quantile, e.g.
// 0.99 → "0.99", 0.5 → "0.5".
func formatQuantile(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

// formatQuantile100 returns the integer-percent form, e.g. 0.99 → "99",
// 0.5 → "50". Used in title templates: "p{{ .Quantile100 }}".
func formatQuantile100(q float64) string {
	return strconv.Itoa(int(q*100 + 0.5))
}

// panelKindFor maps the YAML panel.Kind string to ir.PanelKind. The IR
// today defines stat/graph/timeseries; the DSL also accepts gauge and
// barchart. For coexistence with v0.1 IR, gauge → stat (closest visual)
// and barchart → graph; if/when ir.PanelKind grows, this mapping should
// be refined. Empty defaults to timeseries.
func panelKindFor(kind string) ir.PanelKind {
	switch kind {
	case "stat":
		return ir.PanelKindStat
	case "graph", "barchart":
		return ir.PanelKindGraph
	case "gauge":
		return ir.PanelKindStat
	case "timeseries", "":
		return ir.PanelKindTimeSeries
	}
	return ir.PanelKindTimeSeries
}

// Compile-time check: YAMLRecipe satisfies the Recipe interface.
var _ Recipe = (*YAMLRecipe)(nil)

// MatchesMetricType reports whether the matched metric's Prometheus type
// satisfies a panel's RequiresMetricType filter. Exposed for tests; not
// part of the Recipe interface.
func MatchesMetricType(m inventory.MetricType, want string) bool {
	return want == "" || string(m) == want
}
