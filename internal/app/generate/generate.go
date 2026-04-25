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
	if err := writeOutputs(cfg.OutDir, dashJSON, rationaleMD, warningsJSON); err != nil {
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
// providers — see internal/enrich/factory.go) and then dispatches by the
// returned enricher's Describe() to keep mutation paths in one place.
//
// Today the only enricher that ever returns from the factory is
// NoopEnricher (the {"", "off", "noop"} aliases). Anthropic, OpenAI, and
// any future local provider register their own Constructors in the
// enrich package; when they exist this glue does not need to change —
// only the per-enricher mutation branch below grows.
//
// Provider lookup errors flow up unchanged: ErrUnknownProvider for names
// the registry has never heard of, ErrNotImplementedYet for placeholders
// awaiting Phase 3+ work. Run() wraps the result as ErrBackend.
func applyEnrichment(_ context.Context, d *ir.Dashboard, cfg *config.RunConfig) (*ir.Dashboard, error) {
	enricher, err := enrich.New(enrich.Spec{
		Provider: cfg.Provider,
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

	// No active enricher applies its own mutation logic yet. When the
	// first real provider lands, branch here on desc.Provider and call
	// EnrichTitles / EnrichRationale / ClassifyUnknown per cfg.EnrichModes.
	// All such mutations must respect the V0.2-PLAN §2.2 boundary: never
	// produce PromQL, never upgrade verdicts.
	return d, nil
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

func writeOutputs(dir string, dash, rat, warn []byte) error {
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
		if err := os.WriteFile(filepath.Join(dir, w.name), w.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
