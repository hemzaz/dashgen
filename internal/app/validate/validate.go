// Package validate owns the end-to-end workflow for the `dashgen validate`
// command. It takes one or more PromQL expressions, runs each through the
// staged validator (internal/validate), and emits a JSON report to stdout.
//
// Error categories are surfaced through typed wrappers so cmd/dashgen can
// map them onto non-zero exit codes:
//
//	input_error       — the caller supplied bad CLI input (missing exprs,
//	                    unreadable --from file, conflicting backends)
//	backend_error     — discovery or client construction failed
//	strict_violation  — strict mode saw a warning or refusal
package validate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"dashgen/internal/app/generate"
	"dashgen/internal/config"
	"dashgen/internal/discover"
	"dashgen/internal/ir"
	"dashgen/internal/prometheus"
	"dashgen/internal/safety"
	promvalidate "dashgen/internal/validate"
)

// Error categories. cmd/dashgen matches on these to pick exit codes.
//
// ErrValidateBackend is an alias for generate.ErrBackend so the CLI-level
// exit-code mapping stays identical between `generate` and `validate`.
var (
	ErrValidateInput   = errors.New("input_error")
	ErrValidateBackend = generate.ErrBackend
)

// perQueryTimeout bounds every instant query made by the validator.
// totalBudget caps the number of backend calls per run.
const (
	perQueryTimeout = 5 * time.Second
	totalBudget     = 200
)

// Entry is the shape of one element in the JSON output array.
//
// The zero value for Warnings and RefusalReason is elided from JSON so
// pure-accept entries do not carry empty keys.
type Entry struct {
	Expr          string   `json:"expr"`
	Verdict       string   `json:"verdict"`
	Warnings      []string `json:"warnings,omitempty"`
	RefusalReason string   `json:"refusal_reason,omitempty"`
	ElapsedMs     int64    `json:"elapsed_ms"`
}

// Run executes the validate command. It writes the JSON report to
// os.Stdout. Strict violations are surfaced as a wrapped error after the
// report is emitted so callers still see the full diagnostic.
func Run(ctx context.Context, cfg *config.RunConfig) error {
	return run(ctx, cfg, os.Stdout)
}

// run is the testable core. Output is written to w.
func run(ctx context.Context, cfg *config.RunConfig, w io.Writer) error {
	if cfg == nil {
		return fmt.Errorf("%w: nil config", ErrValidateInput)
	}
	if cfg.PromURL != "" && cfg.FixtureDir != "" {
		return fmt.Errorf("%w: --prom-url and --fixture-dir are mutually exclusive", ErrValidateInput)
	}
	if cfg.PromURL == "" && cfg.FixtureDir == "" {
		return fmt.Errorf("%w: one of --prom-url or --fixture-dir is required", ErrValidateInput)
	}

	exprs, err := collectExprs(cfg)
	if err != nil {
		return err
	}
	if len(exprs) == 0 {
		return fmt.Errorf("%w: at least one expression required (use --expr or --from)", ErrValidateInput)
	}

	client, err := buildClient(cfg)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrValidateBackend, err)
	}
	return runWithClient(ctx, cfg, client, exprs, w)
}

// runWithClient is the post-validation core. Factored out so tests can
// inject a fake prometheus.Client without touching the fixture-directory
// plumbing or CLI-input validation.
func runWithClient(ctx context.Context, cfg *config.RunConfig, client prometheus.Client, exprs []string, w io.Writer) error {
	pipeline := promvalidate.New(client, safety.NewPolicy(nil), promvalidate.Options{
		PerQueryTimeout: perQueryTimeout,
		TotalBudget:     totalBudget,
		Strict:          false,
	})

	entries := make([]Entry, len(exprs))
	for i, expr := range exprs {
		entries[i] = validateOne(ctx, pipeline, expr)
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	// Exit-code semantics:
	//   * any refusal → non-zero generic error (exit 1); the JSON carries
	//     the verdict and reason, but a refusal is a hard "no" so the
	//     process signals failure rather than forcing callers to parse
	//   * --strict AND any warning-or-worse → ErrStrictViolation (exit 4)
	if cfg.Strict {
		for _, e := range entries {
			if e.Verdict == string(ir.VerdictRefuse) {
				return fmt.Errorf("%w: %s: %s", generate.ErrStrictViolation, e.Expr, e.RefusalReason)
			}
			if len(e.Warnings) > 0 {
				return fmt.Errorf("%w: %s: %s", generate.ErrStrictViolation, e.Expr, strings.Join(e.Warnings, ","))
			}
		}
	} else {
		for _, e := range entries {
			if e.Verdict == string(ir.VerdictRefuse) {
				return fmt.Errorf("refused: %s: %s", e.Expr, e.RefusalReason)
			}
		}
	}
	return nil
}

// validateOne runs a single candidate through the pipeline and renders
// the outcome into a stable Entry.
func validateOne(ctx context.Context, pipeline *promvalidate.Pipeline, expr string) Entry {
	candidate := ir.QueryCandidate{Expr: expr}
	start := time.Now()
	res := pipeline.Validate(ctx, &candidate)
	elapsed := time.Since(start)

	warnings := append([]string(nil), res.WarningCodes...)
	sort.Strings(warnings)

	return Entry{
		Expr:          expr,
		Verdict:       string(res.Verdict),
		Warnings:      warnings,
		RefusalReason: res.RefusalReason,
		ElapsedMs:     elapsed.Milliseconds(),
	}
}

// collectExprs merges cfg.Exprs and expressions loaded from cfg.ExprFile,
// preserving caller order: CLI --expr values first, then --from lines.
func collectExprs(cfg *config.RunConfig) ([]string, error) {
	out := make([]string, 0, len(cfg.Exprs))
	for _, e := range cfg.Exprs {
		trimmed := strings.TrimSpace(e)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if cfg.ExprFile == "" {
		return out, nil
	}
	fileExprs, err := readExprFile(cfg.ExprFile)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidateInput, err)
	}
	out = append(out, fileExprs...)
	return out, nil
}

// readExprFile reads one PromQL expression per line from path. Blank
// lines and lines whose first non-whitespace character is `#` are
// skipped.
func readExprFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open --from %s: %w", path, err)
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	// Allow long PromQL expressions; default buffer is 64KiB which is
	// plenty but we make the cap explicit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read --from %s: %w", path, err)
	}
	return out, nil
}

// buildClient returns a prometheus.Client backed by either the fixture
// source or the live HTTP backend. Mirrors generate.buildBackend but
// only needs the client (validate has no discovery step).
func buildClient(cfg *config.RunConfig) (prometheus.Client, error) {
	if cfg.FixtureDir != "" {
		src, err := discover.NewFixtureSource(cfg.FixtureDir)
		if err != nil {
			return nil, err
		}
		return discover.NewFixtureClient(src), nil
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return prometheus.NewClient(cfg.PromURL, timeout), nil
}
