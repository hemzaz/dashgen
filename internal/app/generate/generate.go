// Package generate owns the end-to-end workflow for the `dashgen generate`
// command. It wires discovery → inventory → classification → synthesis →
// validation → post-validation cleanup → rendering → file output.
//
// Error categories are surfaced through typed wrappers so cmd/dashgen can map
// them onto non-zero exit codes:
//
//	backend_error      — discovery or client construction failed
//	render_error       — a renderer rejected the finalized IR
//	strict_violation   — strict mode saw warnings after validation
package generate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"dashgen/internal/classify"
	"dashgen/internal/config"
	"dashgen/internal/discover"
	"dashgen/internal/enrich"
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/prometheus"
	"dashgen/internal/recipes"
	"dashgen/internal/regenerate"
	grafana "dashgen/internal/render/grafana"
	rationale "dashgen/internal/render/rationale"
	warnings "dashgen/internal/render/warnings"
	"dashgen/internal/safety"
	"dashgen/internal/synth"
	"dashgen/internal/validate"
)

// Error categories. cmd/dashgen matches on these to pick exit codes and
// reviewers match on them in rationale output.
var (
	ErrBackend         = errors.New("backend_error")
	ErrRender          = errors.New("render_error")
	ErrStrictViolation = errors.New("strict_violation")
)

// Run executes the full generate pipeline. Exactly one of cfg.PromURL or
// cfg.FixtureDir must be set; the caller (CLI) enforces that invariant and
// this function re-checks it as a defense in depth.
func Run(ctx context.Context, cfg *config.RunConfig) error {
	if cfg == nil {
		return fmt.Errorf("generate: nil config")
	}
	profile := profiles.Profile(cfg.Profile)
	if !profiles.IsKnown(profile) {
		return fmt.Errorf("generate: unknown profile %q", cfg.Profile)
	}
	if cfg.PromURL == "" && cfg.FixtureDir == "" {
		return fmt.Errorf("generate: one of --prom-url or --fixture-dir is required")
	}
	if cfg.PromURL != "" && cfg.FixtureDir != "" {
		return fmt.Errorf("generate: --prom-url and --fixture-dir are mutually exclusive")
	}

	source, client, err := buildBackend(cfg)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBackend, err)
	}

	raw, err := source.Discover(ctx, discover.Selector{
		Job:         cfg.Job,
		Namespace:   cfg.Namespace,
		MetricMatch: cfg.MetricMatch,
	})
	if err != nil {
		return fmt.Errorf("%w: discover: %w", ErrBackend, err)
	}

	inv := rawToInventory(raw)
	inv.Sort()

	classified := classify.Classify(inv)
	var registry *recipes.Registry
	switch profile {
	case profiles.ProfileService:
		registry = recipes.NewServiceRegistry()
	case profiles.ProfileInfra:
		registry = recipes.NewInfraRegistry()
	case profiles.ProfileK8s:
		registry = recipes.NewK8sRegistry()
	default:
		return fmt.Errorf("generate: unsupported profile %q", profile)
	}
	dashboard := synth.SynthesizeWithCap(classified, profile, registry, cfg.MaxPanels)

	policy := safety.NewPolicy(nil)
	// Strict is enforced at the generate layer (after panel assembly) rather
	// than inside the validator so one warning does not cascade into a
	// fully-refused dashboard that the strict check then cannot see.
	pipeline := validate.New(client, policy, validate.Options{
		PerQueryTimeout: 5 * time.Second,
		TotalBudget:     200,
		Strict:          false,
	})

	dashboard = validateAndFinalize(ctx, pipeline, dashboard)

	// v0.2 enrichment seam (V0.2-PLAN.md §2). With cfg.Provider == "" or
	// "off" (the default), this is a no-op pass-through using NoopEnricher
	// and the dashboard is returned unchanged. Phase 3+ adds real providers.
	dashboard, err = applyEnrichment(ctx, dashboard, cfg)
	if err != nil {
		return fmt.Errorf("%w: enrichment: %w", ErrBackend, err)
	}

	// Strict mode: any surviving warning short-circuits before rendering.
	if cfg.Strict {
		if hit := firstStrictWarning(dashboard); hit != "" {
			return fmt.Errorf("%w: %s", ErrStrictViolation, hit)
		}
	}

	dashJSON, err := grafana.Render(dashboard)
	if err != nil {
		return fmt.Errorf("%w: grafana: %w", ErrRender, err)
	}
	rationaleMD, err := rationale.Render(dashboard)
	if err != nil {
		return fmt.Errorf("%w: rationale: %w", ErrRender, err)
	}
	warningsJSON, err := warnings.Render(dashboard)
	if err != nil {
		return fmt.Errorf("%w: warnings: %w", ErrRender, err)
	}

	if cfg.DryRun {
		fmt.Fprintln(os.Stdout, "--- dashboard.json ---")
		os.Stdout.Write(dashJSON)
		fmt.Fprintln(os.Stdout, "\n--- rationale.md ---")
		os.Stdout.Write(rationaleMD)
		fmt.Fprintln(os.Stdout, "\n--- warnings.json ---")
		os.Stdout.Write(warningsJSON)
		fmt.Fprintln(os.Stdout)
		return nil
	}
	if err := writeOutputs(cfg.OutDir, dashJSON, rationaleMD, warningsJSON, cfg.InPlace); err != nil {
		return fmt.Errorf("write outputs: %w", err)
	}
	return nil
}

// buildBackend returns a discovery Source plus the prometheus.Client the
// validate pipeline uses for stage-3 execution. Live mode builds an HTTP
// client; fixture mode loads JSON files into memory once.
func buildBackend(cfg *config.RunConfig) (discover.Source, prometheus.Client, error) {
	if cfg.FixtureDir != "" {
		src, err := discover.NewFixtureSource(cfg.FixtureDir)
		if err != nil {
			return nil, nil, err
		}
		return src, discover.NewFixtureClient(src), nil
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	httpClient := prometheus.NewClient(cfg.PromURL, timeout)
	return discover.NewPrometheusSource(httpClient), httpClient, nil
}

// validateAndFinalize walks every QueryCandidate, runs the validate pipeline,
// updates the candidate with the verdict, then applies SPECS Rule 5: drop
// panels where every candidate refuses and drop sections where every panel
// was dropped.
func validateAndFinalize(ctx context.Context, pipeline *validate.Pipeline, d *ir.Dashboard) *ir.Dashboard {
	if d == nil {
		return d
	}
	out := &ir.Dashboard{
		UID:       d.UID,
		Title:     d.Title,
		Profile:   d.Profile,
		Variables: d.Variables,
		Warnings:  d.Warnings,
	}
	for _, row := range d.Rows {
		newPanels := make([]ir.Panel, 0, len(row.Panels))
		for _, p := range row.Panels {
			panel := p
			newQueries := make([]ir.QueryCandidate, len(panel.Queries))
			copy(newQueries, panel.Queries)
			allRefused := true
			panelWarnings := map[string]struct{}{}
			for i := range newQueries {
				q := &newQueries[i]
				res := pipeline.Validate(ctx, q)
				q.Verdict = res.Verdict
				q.WarningCodes = res.WarningCodes
				if res.RefusalReason != "" {
					q.RefusalReason = res.RefusalReason
				}
				if q.Verdict != ir.VerdictRefuse {
					allRefused = false
				}
				for _, code := range res.WarningCodes {
					panelWarnings[code] = struct{}{}
				}
			}
			panel.Queries = newQueries
			if allRefused && len(newQueries) > 0 {
				// SPECS Rule 5: drop panels where every candidate was refused.
				continue
			}
			panel.Warnings = mergeWarnings(panel.Warnings, panelWarnings)
			panel.Verdict = rolloverVerdict(newQueries)
			newPanels = append(newPanels, panel)
		}
		if len(newPanels) == 0 {
			// SPECS Rule 5: drop sections where all panels were dropped.
			continue
		}
		out.Rows = append(out.Rows, ir.Row{Title: row.Title, Panels: newPanels})
	}
	return out
}

// mergeWarnings folds panelWarnings into the existing slice while preserving
// the original order for any code already present, then appends new codes in
// sorted order so output is deterministic.
func mergeWarnings(existing []string, panelWarnings map[string]struct{}) []string {
	have := map[string]bool{}
	for _, w := range existing {
		have[w] = true
	}
	added := make([]string, 0, len(panelWarnings))
	for w := range panelWarnings {
		if have[w] {
			continue
		}
		added = append(added, w)
	}
	sort.Strings(added)
	return append(existing, added...)
}

// rolloverVerdict is the panel-level composition rule: refuse overrides
// warning overrides accept. Refused queries were already filtered for
// emission by the grafana renderer, but the panel still needs a rollup so
// downstream consumers (CI, reviewers) see the worst non-dropped outcome.
func rolloverVerdict(queries []ir.QueryCandidate) ir.Verdict {
	hasWarning := false
	for _, q := range queries {
		switch q.Verdict {
		case ir.VerdictRefuse:
			// individual refused queries are kept on the panel but we do not
			// upgrade the panel to refused because at least one query
			// survived (otherwise the panel would have been dropped above).
		case ir.VerdictAcceptWithWarning:
			hasWarning = true
		}
	}
	if hasWarning {
		return ir.VerdictAcceptWithWarning
	}
	return ir.VerdictAccept
}

// firstStrictWarning returns a human-readable description of the first
// panel-level warning encountered in strict mode, or "" if none.
func firstStrictWarning(d *ir.Dashboard) string {
	if d == nil {
		return ""
	}
	for _, row := range d.Rows {
		for _, p := range row.Panels {
			if len(p.Warnings) > 0 {
				return fmt.Sprintf("panel %q: %s", p.Title, p.Warnings[0])
			}
			for _, q := range p.Queries {
				if len(q.WarningCodes) > 0 {
					return fmt.Sprintf("query on panel %q: %s", p.Title, q.WarningCodes[0])
				}
			}
		}
	}
	return ""
}

// applyEnrichment is the v0.2 enrichment seam. It delegates provider
// selection to the enrich.New factory (the single extension point for new
// providers — see internal/enrich/factory.go) and then dispatches per
// cfg.EnrichModes to the enricher's EnrichTitles / EnrichRationale methods.
//
// Mutation contract (V0.2-PLAN §2.2):
//   - AI never produces PromQL.
//   - AI never upgrades verdicts.
//   - The only fields a successful enrichment may write are
//     Panel.MechanicalTitle and Panel.RationaleExtra. Both are zero when
//     the deterministic synth path produces a Panel; an already-non-empty
//     value is never overwritten.
//
// Failure contract: provider construction errors (unknown provider, missing
// API key) flow up to Run() as ErrBackend. Per-call enricher errors after
// construction soft-fail: the dispatch logs a single line to stderr and
// degrades to a noop, leaving the dashboard unchanged. ErrBackend wrapping
// at this layer would crash the run, which violates the V0.2-PLAN §2.5
// "graceful degradation" rule.
func applyEnrichment(ctx context.Context, d *ir.Dashboard, cfg *config.RunConfig) (*ir.Dashboard, error) {
	enricher, err := enrich.New(enrich.Spec{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		CacheDir: cfg.CacheDir,
		NoCache:  cfg.NoEnrichCache,
	})
	if err != nil {
		return d, err
	}

	desc := enricher.Describe()
	if desc.Provider == "noop" {
		// Noop path: provider built (audit-trail consumers see Describe()
		// = "noop") and the dashboard is returned untouched. No
		// allocation, no mutation. This is the load-bearing AI-off-parity
		// contract from V0.2-PLAN §6.
		return d, nil
	}

	// Optional debug-only payload preview logging (Step 5.1). Hosted
	// providers (anthropic, openai) implement enrich.PayloadLoggerSetter;
	// the noop path returned above so the type assertion only ever runs
	// against a real provider. The callback fires once per outbound HTTP
	// call with (function, byte count, redacted preview); the preview is
	// derived from wire bytes only, so the redaction guarantee carries
	// through to stderr. The flag is hidden from --help unless
	// DASHGEN_DEBUG=1 to prevent the ADVERSARY §6 drift pattern.
	if cfg.LogEnrichmentPayloads {
		if setter, ok := enricher.(enrich.PayloadLoggerSetter); ok {
			provider := desc.Provider
			setter.SetPayloadLogger(func(fn string, n int, preview string) {
				fmt.Fprintf(os.Stderr, "dashgen[enrich]: provider=%s fn=%s bytes=%d preview=%q\n", provider, fn, n, preview)
			})
		}
	}

	if containsMode(cfg.EnrichModes, "titles") {
		applyTitleEnrichment(ctx, enricher, d)
	}
	if containsMode(cfg.EnrichModes, "rationale") {
		applyRationaleEnrichment(ctx, enricher, d)
	}
	if containsMode(cfg.EnrichModes, "classify") {
		// TODO(phase-5): wire ClassifyUnknown when unknown-grouping coverage lands
	}

	return d, nil
}

// containsMode reports whether `modes` enables `want`. The `none` token is a
// short-circuit that disables every mode regardless of position; `all`
// enables every mode. Empty modes (the v0.2 default) returns false.
func containsMode(modes []string, want string) bool {
	for _, m := range modes {
		if m == "none" {
			return false
		}
	}
	for _, m := range modes {
		if m == want || m == "all" {
			return true
		}
	}
	return false
}

// applyTitleEnrichment dispatches EnrichTitles and writes Panel.MechanicalTitle
// for every panel UID the provider returned a non-empty proposal for. The
// dashboard is mutated only after a successful response, so a provider error
// leaves every panel untouched. Refused panels are excluded from the request.
func applyTitleEnrichment(ctx context.Context, enricher enrich.Enricher, d *ir.Dashboard) {
	var reqs []enrich.PanelTitleRequest
	for _, row := range d.Rows {
		for _, p := range row.Panels {
			if p.Verdict == ir.VerdictRefuse {
				continue
			}
			reqs = append(reqs, enrich.PanelTitleRequest{
				PanelUID:        p.UID,
				MechanicalTitle: p.Title,
				Section:         row.Title,
				Rationale:       p.Rationale,
			})
		}
	}
	if len(reqs) == 0 {
		return
	}
	out, err := enricher.EnrichTitles(ctx, enrich.TitleInput{Requests: reqs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrich titles: %v\n", err)
		return
	}
	byUID := make(map[string]string, len(out.Proposals))
	for _, prop := range out.Proposals {
		if prop.Title == "" {
			continue
		}
		byUID[prop.PanelUID] = prop.Title
	}
	for ri := range d.Rows {
		for pi := range d.Rows[ri].Panels {
			p := &d.Rows[ri].Panels[pi]
			if p.MechanicalTitle != "" {
				// Never overwrite an already-non-empty value.
				continue
			}
			if t, ok := byUID[p.UID]; ok {
				p.MechanicalTitle = t
			}
		}
	}
}

// applyRationaleEnrichment dispatches EnrichRationale and writes
// Panel.RationaleExtra following the same all-or-nothing soft-fail contract
// as applyTitleEnrichment.
func applyRationaleEnrichment(ctx context.Context, enricher enrich.Enricher, d *ir.Dashboard) {
	var reqs []enrich.PanelRationaleRequest
	for _, row := range d.Rows {
		for _, p := range row.Panels {
			if p.Verdict == ir.VerdictRefuse {
				continue
			}
			exprs := make([]string, 0, len(p.Queries))
			for _, q := range p.Queries {
				exprs = append(exprs, q.Expr)
			}
			reqs = append(reqs, enrich.PanelRationaleRequest{
				PanelUID:        p.UID,
				MechanicalTitle: p.Title,
				Section:         row.Title,
				Rationale:       p.Rationale,
				QueryExprs:      exprs,
			})
		}
	}
	if len(reqs) == 0 {
		return
	}
	out, err := enricher.EnrichRationale(ctx, enrich.RationaleInput{Requests: reqs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrich rationale: %v\n", err)
		return
	}
	byUID := make(map[string]string, len(out.Proposals))
	for _, prop := range out.Proposals {
		if prop.Paragraph == "" {
			continue
		}
		byUID[prop.PanelUID] = prop.Paragraph
	}
	for ri := range d.Rows {
		for pi := range d.Rows[ri].Panels {
			p := &d.Rows[ri].Panels[pi]
			if p.RationaleExtra != "" {
				// Never overwrite an already-non-empty value.
				continue
			}
			if para, ok := byUID[p.UID]; ok {
				p.RationaleExtra = para
			}
		}
	}
}

// rawToInventory is a thin adapter from RawInventory to MetricInventory.
func rawToInventory(raw *discover.RawInventory) *inventory.MetricInventory {
	if raw == nil {
		return &inventory.MetricInventory{}
	}
	inv := &inventory.MetricInventory{Metrics: make([]inventory.MetricDescriptor, 0, len(raw.Metrics))}
	for _, m := range raw.Metrics {
		inv.Metrics = append(inv.Metrics, inventory.MetricDescriptor{
			Name:   m.Name,
			Type:   inventory.MetricType(m.Type),
			Help:   m.Help,
			Labels: m.Labels,
		})
	}
	return inv
}

// writeOutputs writes the three output files. With inPlace=true, each
// file is only rewritten when its bytes differ from what's already on
// disk (see internal/regenerate.WriteIfChanged) — preserving mtime on
// no-op runs. With inPlace=false (the v0.1 default), every file is
// written unconditionally.
func writeOutputs(dir string, dash, rat, warn []byte, inPlace bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	writes := []struct {
		name string
		data []byte
	}{
		{"dashboard.json", dash},
		{"rationale.md", rat},
		{"warnings.json", warn},
	}
	for _, w := range writes {
		path := filepath.Join(dir, w.name)
		if inPlace {
			if _, err := regenerate.WriteIfChanged(path, w.data); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(path, w.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
