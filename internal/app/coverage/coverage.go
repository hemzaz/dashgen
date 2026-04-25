// Package coverage owns the end-to-end workflow for the
// `dashgen coverage` subcommand. It reads a fixture-dir inventory
// (metadata.json) and an optional dashboard.json bundle, runs
// internal/coverage.Compute, emits a deterministic JSON report.
//
// Live-Prometheus discovery is intentionally NOT supported in v0.2:
// fixture-dir is the only input mode. Adding live discovery is a
// follow-up that mirrors the generate command's --prom-url branch.
package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"dashgen/internal/coverage"
)

// Error categories. cmd/dashgen matches on these to pick exit codes.
var (
	ErrInput  = errors.New("coverage_input_error")
	ErrRender = errors.New("coverage_render_error")
)

// Config controls a single `dashgen coverage` invocation.
type Config struct {
	// FixtureDir is the directory holding metadata.json. Required.
	FixtureDir string

	// In is an optional dashboard.json bundle directory. When unset,
	// every inventory metric is treated as uncovered (a useful "what
	// could we possibly cover?" view).
	In string

	// Out is where the JSON report is written. Empty means stdout.
	Out string
}

// Run executes the full coverage pipeline.
func Run(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("coverage: nil config")
	}
	if cfg.FixtureDir == "" {
		return fmt.Errorf("%w: --fixture-dir is required", ErrInput)
	}

	inventory, err := loadInventory(filepath.Join(cfg.FixtureDir, "metadata.json"))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInput, err)
	}

	var refs []string
	dashSource := ""
	if cfg.In != "" {
		dashPath := filepath.Join(cfg.In, "dashboard.json")
		dashSource = dashPath
		exprs, err := loadDashboardExprs(dashPath)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInput, err)
		}
		refs = coverage.ExtractReferencedMetrics(inventory, exprs)
	}

	summary, covered, uncovered, families := coverage.Compute(inventory, refs)
	report := coverage.Report{
		SourceInventory: filepath.Join(cfg.FixtureDir, "metadata.json"),
		SourceDashboard: dashSource,
		Summary:         summary,
		Covered:         covered,
		Uncovered:       uncovered,
		UnknownFamilies: families,
	}
	if err := writeReport(cfg.Out, report); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	return nil
}

// loadInventory reads <fixture-dir>/metadata.json and returns the
// sorted list of metric names. The shape is map[name][]any — the
// values are not interesting here, only the keys.
func loadInventory(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// loadDashboardExprs reads dashboard.json and returns every
// target.expr across every non-row panel.
func loadDashboardExprs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw struct {
		Panels []struct {
			Type    string `json:"type"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var exprs []string
	for _, p := range raw.Panels {
		if p.Type == "row" {
			continue
		}
		for _, t := range p.Targets {
			if t.Expr == "" {
				continue
			}
			exprs = append(exprs, t.Expr)
		}
	}
	return exprs, nil
}

// writeReport emits the report as pretty-printed JSON to either out
// (a file path) or stdout (when out is empty). 2-space indent +
// trailing newline matches the lint command's convention.
func writeReport(out string, r coverage.Report) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	body = append(body, '\n')
	if out == "" {
		_, err := os.Stdout.Write(body)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(out), err)
	}
	return os.WriteFile(out, body, 0o644)
}
