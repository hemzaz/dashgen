// Package lint owns the end-to-end workflow for the `dashgen lint`
// subcommand: read an existing dashboard.json bundle, run every
// registered check from internal/lint, emit a deterministic JSON report.
//
// Error categories are surfaced through typed wrappers so cmd/dashgen
// can map them onto non-zero exit codes:
//
//	input_error  — the bundle directory is unreadable or malformed
//	render_error — emitting the report failed
//	lint_failure — at least one check reported SeverityRefuse
package lint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dashgen/internal/lint"
)

// Error categories. cmd/dashgen matches on these to pick exit codes.
var (
	ErrInput       = errors.New("input_error")
	ErrRender      = errors.New("render_error")
	ErrLintFailure = errors.New("lint_failure")
)

// Config controls a single `dashgen lint` invocation. Kept small and
// flat: the CLI builds it directly from cobra flags.
type Config struct {
	// In is the directory containing dashboard.json (and, in the full
	// Step 3.0 spec, rationale.md and warnings.json). Required.
	In string

	// Out is where the JSON report is written. Empty means stdout.
	Out string
}

// Report is the JSON document `dashgen lint` emits. The shape is the
// public contract; field names (json tags) are part of the API and
// changing them is a breaking change.
type Report struct {
	Source string       `json:"source"` // dashboard.json path used
	Issues []lint.Issue `json:"issues"`
}

// Run executes the full lint pipeline. Returns ErrLintFailure if any
// registered check reported SeverityRefuse; the caller wraps it for
// the CLI exit code. Other error categories signal pipeline (not
// dashboard-quality) failures.
func Run(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("lint: nil config")
	}
	if cfg.In == "" {
		return fmt.Errorf("%w: --in is required", ErrInput)
	}
	dashPath := filepath.Join(cfg.In, "dashboard.json")
	panels, err := loadDashboard(dashPath)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInput, err)
	}
	rationalePath := filepath.Join(cfg.In, "rationale.md")
	rationale, err := loadRationale(rationalePath)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInput, err)
	}

	in := &lint.Input{Panels: panels, Rationale: rationale}
	issues := lint.RunAll(in)
	report := Report{Source: dashPath, Issues: issues}

	if err := writeReport(cfg.Out, report); err != nil {
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	if lint.HasRefusal(issues) {
		return fmt.Errorf("%w: %d refusal(s) in %s", ErrLintFailure, refusalCount(issues), dashPath)
	}
	return nil
}

// rawPanel mirrors the on-disk Grafana schema we care about. We
// decode into this shape and flatten to lint.Panel so the lint
// package never has to know about Grafana's nested fieldConfig.
type rawPanel struct {
	ID          int64         `json:"id"`
	Type        string        `json:"type"`
	Title       string        `json:"title"`
	Targets     []lint.Target `json:"targets"`
	FieldConfig struct {
		Defaults struct {
			Unit string `json:"unit"`
		} `json:"defaults"`
	} `json:"fieldConfig"`
}

// loadDashboard reads dashboard.json and decodes the subset of fields
// lint cares about. Absent/extra fields in the file are tolerated; only
// the panel array shape matters.
func loadDashboard(path string) ([]lint.Panel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw struct {
		Panels []rawPanel `json:"panels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	flat := make([]lint.Panel, 0, len(raw.Panels))
	for _, p := range raw.Panels {
		flat = append(flat, lint.Panel{
			ID:      p.ID,
			Type:    p.Type,
			Title:   p.Title,
			Unit:    p.FieldConfig.Defaults.Unit,
			Targets: p.Targets,
		})
	}
	return flat, nil
}

// loadRationale reads rationale.md from the bundle directory if
// present. The file is optional from lint's perspective: a missing
// file simply disables checks that need it (Input.Rationale stays
// "") rather than failing the run with ErrInput. Other read errors
// (permission denied, malformed bytes) are surfaced.
func loadRationale(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// writeReport emits the report as JSON to either out (a file path) or
// stdout (when out is empty). Output is pretty-printed with stable
// 2-space indent so diffs read cleanly in PRs, and a trailing newline
// is appended so editors don't complain.
func writeReport(out string, r Report) error {
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

func refusalCount(issues []lint.Issue) int {
	n := 0
	for _, i := range issues {
		if i.Severity == lint.SeverityRefuse {
			n++
		}
	}
	return n
}
