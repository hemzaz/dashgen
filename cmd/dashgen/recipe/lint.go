package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"dashgen/internal/recipes"
)

// Sentinel errors for exit-code mapping in main.exitCodeFor.
// Each maps to a distinct exit code per RECIPES-CLI.md §6.
var (
	// ErrLintFailure is returned when one or more recipe files fail validation.
	// main.exitCodeFor maps this to exit code 1.
	ErrLintFailure = errors.New("recipe lint failed: one or more files invalid")

	// ErrLintInputError is returned when a file cannot be found or opened.
	// main.exitCodeFor maps this to exit code 2.
	ErrLintInputError = errors.New("recipe lint failed: file not found or unreadable")

	// ErrLintResourceLimit is returned when a per-invocation resource cap is
	// exceeded (file count CT2, file size T1, per-file deadline CT9).
	// main.exitCodeFor maps this to exit code 5.
	ErrLintResourceLimit = errors.New("recipe lint failed: resource limit exceeded")
)

// adversary: CT2 — per-invocation file count cap (recipe-list explosion).
// Exceeded → ErrLintResourceLimit (exit code 5).
const lintMaxFilesPerInvocation = 4096

// adversary: CT9 — per-file wall-clock budget (lint as DoS vector).
// Exceeded → file marked invalid; loader returns ErrCodeDeadline.
const lintPerFileDeadline = 5 * time.Second

// canonicalUnits is the set of unit values accepted without a lint warning.
// Non-canonical units pass the schema but emit a "non_canonical_unit" warning
// (promoted to error with --strict). Extend this list when the recipe catalog
// adopts new units.
var canonicalUnits = map[string]bool{
	"ops/sec":    true,
	"errors/sec": true,
	"seconds":    true,
	"bytes":      true,
	"bytes/sec":  true,
	"ratio":      true,
	"percent":    true,
	"short":      true,
	"iops":       true,
	"count":      true,
	"days":       true,
}

// secretScanPatterns is the placeholder pattern set for --secret-scan (CT9 / DSL T18).
// Each pattern is matched against query_template, legend_template, and
// title_template strings. A match emits a "potential_secret" warning (error
// in --strict mode).
//
// SEAM: replace with a production-grade library (e.g. zricethezav/gitleaks
// core patterns) during Phase E adversary-hardening. The opt-in flag and
// per-template scan loop are already wired; only this slice needs expansion.
var secretScanPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|secret|api[_\-]?key|auth[_\-]?token)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`),
}

// lintIssue is one entry in the errors or warnings list for a file.
type lintIssue struct {
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// lintFileResult holds the aggregate outcome for one recipe file.
type lintFileResult struct {
	File      string      `json:"file"`
	Valid     bool        `json:"valid"`
	ElapsedMs int64       `json:"elapsed_ms"`
	Errors    []lintIssue `json:"errors"`
	Warnings  []lintIssue `json:"warnings"`
}

type lintFlags struct {
	strict     bool
	secretScan bool
	quiet      bool
	outputFmt  string
}

func newLintCmd() *cobra.Command {
	var f lintFlags

	cmd := &cobra.Command{
		Use:   "lint <file...>",
		Short: "Validate recipe YAML files against the schema",
		Long: `Validate one or more recipe *.yaml files against the dashgen recipe schema.

NOTE: this validates recipe source files. To audit rendered dashboard bundles
use the top-level "dashgen lint" command instead.

Examples:
  dashgen recipe lint mycorp_queue_depth.yaml
  dashgen recipe lint --strict --secret-scan ./recipes/*.yaml
  find . -name '*.yaml' | xargs dashgen recipe lint --output json

Exit codes:
  0  all files valid
  1  one or more files failed validation
  2  file not found or unreadable
  5  resource limit exceeded (too many files, file too large, deadline hit)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLint(cmd, f, args)
		},
	}

	cmd.Flags().BoolVar(&f.strict, "strict", false,
		"promote warnings to errors (non-canonical units, potential secrets)")
	cmd.Flags().BoolVar(&f.secretScan, "secret-scan", false,
		"scan query/legend/title templates for potential secrets (opt-in)")
	cmd.Flags().BoolVar(&f.quiet, "quiet", false,
		"suppress per-file OK lines; only print failures")
	cmd.Flags().StringVar(&f.outputFmt, "output", "text",
		"output format: text or json")

	return cmd
}

func runLint(cmd *cobra.Command, f lintFlags, files []string) error {
	// CT2: per-invocation file count cap.
	if len(files) > lintMaxFilesPerInvocation {
		return fmt.Errorf("%w: too many files (%d > %d)",
			ErrLintResourceLimit, len(files), lintMaxFilesPerInvocation)
	}

	results := make([]lintFileResult, 0, len(files))

	// priority tracks the highest-severity outcome seen across all files.
	// 5 = resource limit, 2 = input error, 1 = invalid recipe, 0 = all OK.
	priority := 0

	for _, file := range files {
		res, p := lintOneFile(file, f)
		results = append(results, res)
		if p > priority {
			priority = p
		}
	}

	if f.outputFmt == "json" {
		return emitLintJSON(cmd, results, priority)
	}
	return emitLintText(cmd, results, f.quiet, priority)
}

// lintOneFile runs the loader pipeline on a single file and returns the
// result plus a priority integer:
//
//	0 = valid (no errors, possibly warnings)
//	1 = schema/template validation failure
//	2 = file not found or unreadable (I/O error)
//	5 = resource limit exceeded (size cap, deadline, file count)
func lintOneFile(file string, f lintFlags) (lintFileResult, int) {
	start := time.Now()

	res := lintFileResult{
		File:     file,
		Valid:    true,
		Errors:   []lintIssue{},
		Warnings: []lintIssue{},
	}

	// CT9: per-file wall-clock budget. The loader's internal CUEDeadline is
	// set to the same value as a second layer of defense.
	ctx, cancel := context.WithTimeout(context.Background(), lintPerFileDeadline)
	defer cancel()

	cfg := recipes.LoaderConfig{
		CUEDeadline: lintPerFileDeadline,
	}

	loaded, err := recipes.LoadFile(ctx, cfg, file, recipes.SourceUser)
	res.ElapsedMs = time.Since(start).Milliseconds()

	if err != nil {
		res.Valid = false
		issue, p := issueFromLoadError(err)
		res.Errors = append(res.Errors, issue)
		return res, p
	}

	// Post-load checks: collect warnings and optionally promote to errors.
	warnings := collectWarnings(loaded, f.secretScan)
	if f.strict && len(warnings) > 0 {
		res.Valid = false
		res.Errors = append(res.Errors, warnings...)
		return res, 1
	}
	if len(warnings) > 0 {
		res.Warnings = warnings
	}

	return res, 0
}

// issueFromLoadError translates a loader error (typically *recipes.LoadError)
// into a lintIssue and the corresponding CLI priority (1/2/5).
func issueFromLoadError(err error) (lintIssue, int) {
	var lerr *recipes.LoadError
	if errors.As(err, &lerr) {
		issue := lintIssue{
			Line:    lerr.Line,
			Col:     lerr.Col,
			Code:    lerr.Code,
			Message: lerr.Message,
		}
		switch lerr.Code {
		case recipes.ErrCodeFileSize,
			recipes.ErrCodeFileCount,
			recipes.ErrCodeTotalBytes,
			recipes.ErrCodeDeadline:
			return issue, 5
		case recipes.ErrCodeIO:
			return issue, 2
		default:
			return issue, 1
		}
	}
	// Non-LoadError fallback (e.g. unexpected internal error).
	return lintIssue{
		Code:    "internal",
		Message: err.Error(),
	}, 1
}

// collectWarnings inspects a successfully-loaded recipe for conditions that
// warrant a lint warning: non-canonical units and potential secrets in
// template strings.
func collectWarnings(loaded recipes.LoadedRecipe, secretScan bool) []lintIssue {
	var out []lintIssue

	for _, panel := range loaded.Spec.Panels {
		if panel.Unit != "" && !canonicalUnits[panel.Unit] {
			out = append(out, lintIssue{
				Code: "non_canonical_unit",
				Message: fmt.Sprintf(
					"unit %q is not in the canonical set; consider one of: "+
						"ops/sec, errors/sec, seconds, bytes, bytes/sec, "+
						"ratio, percent, short, iops, count, days",
					panel.Unit,
				),
			})
		}

		if secretScan {
			templates := []struct {
				name string
				val  string
			}{
				{"query_template", panel.QueryTemplate},
				{"legend_template", panel.LegendTemplate},
				{"title_template", panel.TitleTemplate},
			}
			for _, tmpl := range templates {
				if tmpl.val == "" {
					continue
				}
				for _, pat := range secretScanPatterns {
					if pat.MatchString(tmpl.val) {
						out = append(out, lintIssue{
							Code: "potential_secret",
							Message: fmt.Sprintf(
								"potential secret pattern in %s (matched /%s/); "+
									"review before publishing",
								tmpl.name, pat.String(),
							),
						})
						break // one finding per template field
					}
				}
			}
		}
	}

	return out
}

// emitLintText renders results in the human-readable text format to
// cmd.OutOrStdout(). Returns the appropriate sentinel error for the
// given priority, or nil when priority == 0.
func emitLintText(cmd *cobra.Command, results []lintFileResult, quiet bool, priority int) error {
	w := cmd.OutOrStdout()

	for _, r := range results {
		if r.Valid && len(r.Warnings) == 0 {
			if !quiet {
				fmt.Fprintf(w, "%s: OK\n", r.File)
			}
			continue
		}

		for _, e := range r.Errors {
			switch {
			case e.Line > 0 && e.Col > 0:
				fmt.Fprintf(w, "%s:%d:%d: %s\n", r.File, e.Line, e.Col, e.Message)
			case e.Line > 0:
				fmt.Fprintf(w, "%s:%d: %s\n", r.File, e.Line, e.Message)
			default:
				fmt.Fprintf(w, "%s: %s\n", r.File, e.Message)
			}
		}

		for _, wn := range r.Warnings {
			switch {
			case wn.Line > 0 && wn.Col > 0:
				fmt.Fprintf(w, "%s:%d:%d: warning: %s\n", r.File, wn.Line, wn.Col, wn.Message)
			case wn.Line > 0:
				fmt.Fprintf(w, "%s:%d: warning: %s\n", r.File, wn.Line, wn.Message)
			default:
				fmt.Fprintf(w, "%s: warning: %s\n", r.File, wn.Message)
			}
		}
	}

	return sentinelForPriority(priority)
}

// emitLintJSON renders results as a JSON array per Appendix B of RECIPES-CLI.md
// and returns the appropriate sentinel error for exit code mapping.
func emitLintJSON(cmd *cobra.Command, results []lintFileResult, priority int) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("encode lint results: %w", err)
	}
	return sentinelForPriority(priority)
}

// sentinelForPriority maps a priority integer to the correct exported sentinel
// error that main.exitCodeFor will translate to the right exit code.
func sentinelForPriority(priority int) error {
	switch priority {
	case 5:
		return ErrLintResourceLimit
	case 2:
		return ErrLintInputError
	case 1:
		return ErrLintFailure
	default:
		return nil
	}
}
