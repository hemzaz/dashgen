// Package inspect owns the `dashgen inspect` workflow. It runs the same
// discovery → classify → synth → validate pipeline that `generate` uses but,
// instead of emitting dashboard/rationale/warnings files, prints a
// human-readable report of the internal state to an io.Writer.
//
// The report surfaces:
//   - inventory summary (metric count, by-type counts, top families)
//   - classification (metric count + trait summary + sample rows)
//   - recipe registry and which recipes matched this run
//   - per-candidate verdicts and warning codes from the validate pipeline
//   - a summary line with accept/warning/refuse counts and omitted sections
//
// Determinism: identical inputs → identical output bytes. No map iteration
// leaks; all slices are sorted before formatting.
package inspect

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"dashgen/internal/classify"
	"dashgen/internal/config"
	"dashgen/internal/discover"
	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
	"dashgen/internal/prometheus"
	"dashgen/internal/recipes"
	"dashgen/internal/safety"
	"dashgen/internal/synth"
	"dashgen/internal/validate"
)

// Run executes the inspect pipeline and writes the report to os.Stdout.
func Run(ctx context.Context, cfg *config.RunConfig) error {
	return RunTo(ctx, cfg, os.Stdout)
}

// RunTo is like Run but writes the report to w. Tests inject a bytes.Buffer.
func RunTo(ctx context.Context, cfg *config.RunConfig, w io.Writer) error {
	if cfg == nil {
		return fmt.Errorf("inspect: nil config")
	}
	if cfg.Profile == "" {
		cfg.Profile = string(profiles.ProfileService)
	}
	profile := profiles.Profile(cfg.Profile)
	if !profiles.IsKnown(profile) {
		return fmt.Errorf("inspect: unknown profile %q", cfg.Profile)
	}
	if cfg.PromURL == "" && cfg.FixtureDir == "" {
		return fmt.Errorf("inspect: one of --prom-url or --fixture-dir is required")
	}
	if cfg.PromURL != "" && cfg.FixtureDir != "" {
		return fmt.Errorf("inspect: --prom-url and --fixture-dir are mutually exclusive")
	}

	source, client, err := buildBackend(cfg)
	if err != nil {
		return fmt.Errorf("inspect: backend: %w", err)
	}

	raw, err := source.Discover(ctx, discover.Selector{
		Job:         cfg.Job,
		Namespace:   cfg.Namespace,
		MetricMatch: cfg.MetricMatch,
	})
	if err != nil {
		return fmt.Errorf("inspect: discover: %w", err)
	}

	inv := rawToInventory(raw)
	inv.Sort()
	classified := classify.Classify(inv)

	registry, profileNote := pickRegistry(profile)
	dashboard := synth.SynthesizeWithCap(classified, profile, registry, cfg.MaxPanels)

	pipeline := validate.New(client, safety.NewPolicy(nil), validate.Options{
		PerQueryTimeout: 5 * time.Second,
		TotalBudget:     200,
	})

	src := sourceDescription(cfg)
	candidates := collectCandidates(ctx, pipeline, dashboard)

	report := &report{
		Profile:     string(profile),
		ProfileNote: profileNote,
		Source:      src,
		Inventory:   inv,
		Classified:  classified,
		Registry:    registry,
		Candidates:  candidates,
		OmittedRows: omittedSections(profile, dashboard),
	}
	return report.writeTo(w)
}

// buildBackend mirrors generate.buildBackend: live HTTP client for --prom-url,
// in-memory fixture for --fixture-dir.
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

// pickRegistry returns the registry for a profile. The returned note is
// non-empty when the caller's requested profile fell back to service because
// the requested registry is not available.
func pickRegistry(p profiles.Profile) (*recipes.Registry, string) {
	switch p {
	case profiles.ProfileService:
		return recipes.NewServiceRegistry(), ""
	case profiles.ProfileInfra:
		return recipes.NewInfraRegistry(), ""
	case profiles.ProfileK8s:
		return recipes.NewK8sRegistry(), ""
	}
	return recipes.NewServiceRegistry(), fmt.Sprintf("unknown profile %q; falling back to service", p)
}

// sourceDescription returns a short human-readable label for the backend used.
func sourceDescription(cfg *config.RunConfig) string {
	if cfg.FixtureDir != "" {
		return "fixture:" + cfg.FixtureDir
	}
	return "prom:" + cfg.PromURL
}

// rawToInventory mirrors generate.rawToInventory.
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

// candidateRow is one emitted row in the Candidates table.
type candidateRow struct {
	Section  string
	PanelUID string
	Expr     string
	Verdict  ir.Verdict
	Warnings []string
	Refusal  string
}

// collectCandidates walks the pre-validation dashboard, runs each
// QueryCandidate through the validate pipeline, and returns the flat list of
// rows sorted by (section, panel_uid).
func collectCandidates(ctx context.Context, pipeline *validate.Pipeline, d *ir.Dashboard) []candidateRow {
	if d == nil {
		return nil
	}
	var rows []candidateRow
	for _, row := range d.Rows {
		for _, panel := range row.Panels {
			for i := range panel.Queries {
				q := panel.Queries[i]
				res := pipeline.Validate(ctx, &q)
				rows = append(rows, candidateRow{
					Section:  row.Title,
					PanelUID: panel.UID,
					Expr:     q.Expr,
					Verdict:  res.Verdict,
					Warnings: append([]string(nil), res.WarningCodes...),
					Refusal:  res.RefusalReason,
				})
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Section != rows[j].Section {
			return rows[i].Section < rows[j].Section
		}
		return rows[i].PanelUID < rows[j].PanelUID
	})
	return rows
}

// omittedSections returns the profile sections not represented in the
// synthesized dashboard (typically because no recipe produced a panel).
func omittedSections(p profiles.Profile, d *ir.Dashboard) []string {
	present := map[string]bool{}
	if d != nil {
		for _, row := range d.Rows {
			present[row.Title] = true
		}
	}
	var out []string
	for _, s := range profiles.Sections(p) {
		if !present[s] {
			out = append(out, s)
		}
	}
	return out
}

// report holds the fully computed inspection state. writeTo formats it.
type report struct {
	Profile     string
	ProfileNote string
	Source      string
	Inventory   *inventory.MetricInventory
	Classified  *classify.ClassifiedInventory
	Registry    *recipes.Registry
	Candidates  []candidateRow
	OmittedRows []string
}

func (r *report) writeTo(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Inspection: %s profile against %s\n", r.Profile, r.Source)
	if r.ProfileNote != "" {
		fmt.Fprintf(&b, "  note: %s\n", r.ProfileNote)
	}
	b.WriteString("\n")
	writeInventory(&b, r.Inventory)
	b.WriteString("\n")
	writeClassification(&b, r.Classified)
	b.WriteString("\n")
	writeRecipes(&b, r.Registry, r.Classified)
	b.WriteString("\n")
	writeCandidates(&b, r.Candidates)
	b.WriteString("\n")
	writeSummary(&b, r.Candidates, r.OmittedRows)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeInventory emits the Inventory section.
func writeInventory(b *strings.Builder, inv *inventory.MetricInventory) {
	b.WriteString("Inventory\n")
	if inv == nil || len(inv.Metrics) == 0 {
		b.WriteString("  metrics: 0\n")
		return
	}
	fmt.Fprintf(b, "  metrics: %d\n", len(inv.Metrics))
	// Count by type using the descriptor's own type (pre-classification).
	counts := map[inventory.MetricType]int{}
	for _, m := range inv.Metrics {
		t := m.Type
		if t == "" {
			t = inventory.MetricTypeUnknown
		}
		counts[t]++
	}
	fmt.Fprintf(b, "  by type: counter=%d  gauge=%d  histogram=%d  summary=%d  unknown=%d\n",
		counts[inventory.MetricTypeCounter],
		counts[inventory.MetricTypeGauge],
		counts[inventory.MetricTypeHistogram],
		counts[inventory.MetricTypeSummary],
		counts[inventory.MetricTypeUnknown],
	)
	// Top families by size. Deterministic tie-break: count desc, name asc.
	families := map[string]int{}
	for _, m := range inv.Metrics {
		fam := familyOf(m.Name)
		families[fam]++
	}
	type famCount struct {
		name string
		n    int
	}
	famList := make([]famCount, 0, len(families))
	for name, n := range families {
		famList = append(famList, famCount{name: name, n: n})
	}
	sort.SliceStable(famList, func(i, j int) bool {
		if famList[i].n != famList[j].n {
			return famList[i].n > famList[j].n
		}
		return famList[i].name < famList[j].name
	})
	if len(famList) > 10 {
		famList = famList[:10]
	}
	parts := make([]string, 0, len(famList))
	for _, f := range famList {
		parts = append(parts, fmt.Sprintf("%s(%d)", f.name, f.n))
	}
	fmt.Fprintf(b, "  families (top 10 by size): %s\n", strings.Join(parts, " "))
}

// familyOf returns the prefix up to the first "_". Matches classify's rule
// without importing the private helper.
func familyOf(name string) string {
	if name == "" {
		return ""
	}
	if i := strings.Index(name, "_"); i >= 0 {
		return name[:i]
	}
	return name
}

// writeClassification emits the Classification section.
func writeClassification(b *strings.Builder, c *classify.ClassifiedInventory) {
	b.WriteString("Classification\n")
	if c == nil || len(c.Metrics) == 0 {
		b.WriteString("  classified: 0\n")
		return
	}
	classifiedCount := 0
	traitCounts := map[string]int{}
	for _, m := range c.Metrics {
		if m.Type != inventory.MetricTypeUnknown {
			classifiedCount++
		}
		for _, t := range m.Traits {
			traitCounts[string(t)]++
		}
	}
	traitKeys := make([]string, 0, len(traitCounts))
	for k := range traitCounts {
		traitKeys = append(traitKeys, k)
	}
	sort.Strings(traitKeys)
	traitParts := make([]string, 0, len(traitKeys))
	for _, k := range traitKeys {
		traitParts = append(traitParts, fmt.Sprintf("%s=%d", k, traitCounts[k]))
	}
	traits := "(none)"
	if len(traitParts) > 0 {
		traits = strings.Join(traitParts, "  ")
	}
	fmt.Fprintf(b, "  %d classified, traits: %s\n", classifiedCount, traits)
	b.WriteString("  sample:\n")

	// Sample rows: up to 20 entries, sorted by Name (already the invariant
	// on c.Metrics). Tabwriter pads columns.
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	limit := 20
	if len(c.Metrics) < limit {
		limit = len(c.Metrics)
	}
	for i := 0; i < limit; i++ {
		m := c.Metrics[i]
		traits := make([]string, 0, len(m.Traits))
		for _, t := range m.Traits {
			traits = append(traits, string(t))
		}
		sort.Strings(traits)
		fmt.Fprintf(tw, "    %s\t%s\tfamily=%s\ttraits=[%s]\n",
			m.Descriptor.Name,
			string(m.Type),
			m.Family,
			strings.Join(traits, ","),
		)
	}
	_ = tw.Flush()
}

// writeRecipes emits the Recipes section: the full registered set plus the
// subset that matched at least one metric in this run.
func writeRecipes(b *strings.Builder, reg *recipes.Registry, c *classify.ClassifiedInventory) {
	b.WriteString("Recipes\n")
	if reg == nil {
		b.WriteString("  registered: 0\n")
		return
	}
	all := reg.All()
	fmt.Fprintf(b, "  registered: %d\n", len(all))
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, r := range all {
		fmt.Fprintf(tw, "    %s\tsection=%s\n", r.Name(), r.Section())
	}
	_ = tw.Flush()

	// Compute which recipes matched any metric. Use recipe's Match function
	// with the same ClassifiedMetricView recipes see in synth.
	b.WriteString("  matched this run:\n")
	views := viewsOf(c)
	type match struct {
		name    string
		metrics []string
	}
	var matched []match
	for _, r := range all {
		var hits []string
		for _, v := range views {
			if r.Match(v) {
				hits = append(hits, v.Descriptor.Name)
			}
		}
		if len(hits) > 0 {
			sort.Strings(hits)
			matched = append(matched, match{name: r.Name(), metrics: hits})
		}
	}
	if len(matched) == 0 {
		b.WriteString("    (none)\n")
		return
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].name < matched[j].name
	})
	tw = tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, m := range matched {
		fmt.Fprintf(tw, "    %s\t-> %s\n", m.name, strings.Join(m.metrics, ", "))
	}
	_ = tw.Flush()
}

// viewsOf returns the same ClassifiedMetricView slice synth gives recipes.
func viewsOf(c *classify.ClassifiedInventory) []recipes.ClassifiedMetricView {
	if c == nil {
		return nil
	}
	out := make([]recipes.ClassifiedMetricView, 0, len(c.Metrics))
	for _, m := range c.Metrics {
		traits := make([]string, 0, len(m.Traits))
		for _, t := range m.Traits {
			traits = append(traits, string(t))
		}
		unit := m.Unit
		if unit == "" {
			unit = m.Descriptor.InferredUnit
		}
		out = append(out, recipes.ClassifiedMetricView{
			Descriptor: m.Descriptor,
			Type:       m.Type,
			Family:     m.Family,
			Unit:       unit,
			Traits:     traits,
		})
	}
	return out
}

// writeCandidates emits the Candidates table.
func writeCandidates(b *strings.Builder, rows []candidateRow) {
	b.WriteString("Candidates\n")
	if len(rows) == 0 {
		b.WriteString("  (none)\n")
		return
	}
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", "section", "panel_uid", "expr", "verdict", "warnings")
	for _, r := range rows {
		warnings := strings.Join(r.Warnings, ",")
		if warnings == "" && r.Refusal != "" {
			warnings = r.Refusal
		}
		if warnings == "" {
			warnings = "-"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
			r.Section,
			r.PanelUID,
			r.Expr,
			string(r.Verdict),
			warnings,
		)
	}
	_ = tw.Flush()
}

// writeSummary emits the final Summary block.
func writeSummary(b *strings.Builder, rows []candidateRow, omitted []string) {
	b.WriteString("Summary\n")
	var accepted, withWarnings, refused int
	for _, r := range rows {
		switch r.Verdict {
		case ir.VerdictAccept:
			accepted++
		case ir.VerdictAcceptWithWarning:
			withWarnings++
		case ir.VerdictRefuse:
			refused++
		}
	}
	fmt.Fprintf(b, "  accepted: %d  with_warnings: %d  refused: %d\n",
		accepted, withWarnings, refused)
	if len(omitted) > 0 {
		fmt.Fprintf(b, "  omitted sections: %s\n", strings.Join(omitted, ", "))
	} else {
		b.WriteString("  omitted sections: (none)\n")
	}
}
