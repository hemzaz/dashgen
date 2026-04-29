package recipes

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

const (
	// maxTemplateOutputBytes is the per-render output cap.
	// adversary: T6 — render-time DoS. Any rendered PromQL string
	// exceeding this is rejected with ErrTemplateOutputTooLarge.
	maxTemplateOutputBytes = 16 * 1024 // 16 KB

	// maxASTNodes is the per-template node-count budget.
	// adversary: T5 — template parse-bomb via deeply nested ASTs.
	maxASTNodes = 256
)

var (
	// ErrForbiddenDirective is returned when a template contains {{ define }},
	// {{ template }}, or {{ block }}. Forbidden per DSL §7.5 and ADVERSARY T5.
	ErrForbiddenDirective = errors.New("template: forbidden directive (define/template/block not allowed)")

	// ErrTemplateOutputTooLarge is returned when rendered output exceeds 16 KB.
	// See ADVERSARY T6.
	ErrTemplateOutputTooLarge = errors.New("template: rendered output exceeds 16 KB cap")

	// ErrASTNodeBudgetExceeded is returned when a parsed template exceeds 256
	// AST nodes. See ADVERSARY T5.
	ErrASTNodeBudgetExceeded = errors.New("template: AST node count exceeds 256-node budget")
)

// RenderContext is the dot-context for every text/template render.
// Constructed once per (matched-metric, panel-template) pair at synth time.
type RenderContext struct {
	// Metric is the matched metric's name (7-bit ASCII; never user-controlled
	// at render time per invariant RC3).
	Metric string

	// Type is the metric's classifier-emitted type ("counter", "gauge",
	// "histogram", "summary").
	Type string

	// Labels carries label NAMES only — never values. Used with the stdlib
	// `index` builtin: {{ index .Labels "mountpoint" }}. Values are
	// deterministic placeholders, never real label values (invariant I2 / RC1).
	Labels map[string]string

	// LabelList is the sorted slice of label names (invariant RC2).
	LabelList []string

	// ScopeFilter is the pre-rendered Prometheus matcher fragment
	// (e.g. `job="$job", instance="$instance"`). Templates embed it verbatim.
	ScopeFilter string

	// Window is the rate window for this panel (e.g. "5m"). Pre-validated
	// against #RateWindow regex (invariant RC4).
	Window string

	// GroupBy is the resolved safeGroupLabels result — computed before render
	// so templates use the helper groupBy . without re-running safety logic.
	GroupBy []string

	// PreferredLabels is the panel.preferred_labels list (post-defaults).
	PreferredLabels []string

	// Quantile is the current quantile (e.g. "0.99"). Empty when the panel
	// does not declare quantiles (invariant RC4 — missingkey=error catches
	// templates that reference this on non-quantile panels).
	Quantile string

	// Quantile100 is Quantile × 100, integer-formatted ("99" for 0.99).
	// Used in title_template: "HTTP latency p{{ .Quantile100 }}".
	Quantile100 string

	// Quantile2 is Quantile fixed-precision-formatted to two decimals
	// ("0.50", "0.95", "0.99"). Required by histogram_quantile templates that
	// need byte-stable %.2f output to match v0.1/v0.2 Go-recipe goldens.
	Quantile2 string

	// Pair is non-nil iff pair_with was declared and resolution succeeded
	// (invariant RC5). Panels with requires_pair: true are skipped before
	// render when Pair is nil.
	Pair *PairContext
}

// PairContext is the dot-context for the pair half of a Tier-C recipe.
// Like RenderContext.Labels, PairContext.Labels is names-only (invariant I2).
type PairContext struct {
	Name   string            // resolved pair metric name
	Type   string            // pair metric's classifier type
	Labels map[string]string // names-only (I2)
}

// Template is a parsed, validated text/template ready for rendering.
type Template struct {
	tpl *template.Template
}

// helperFuncMap returns the closed FuncMap for DSL template rendering.
//
// CLOSED NAMESPACE: exactly the helpers below ship in v0.3.
// To add a helper, file a docs PR updating docs/RECIPES-DSL-HELPERS.md §4
// with ≥2 demand cases. See §1 of that doc for the change process.
//
// BANNED CATEGORIES (must never be added — docs/RECIPES-DSL-HELPERS.md §2):
//   - now/time/today: non-deterministic (clock access breaks byte-equality)
//   - env/getenv: non-hermetic (machine-dependent; secrets leak via T18)
//   - exec/system/shellOut: RCE surface for installed recipe packs
//   - readFile/loadFile/slurp: sandbox violation (recipes are pure transforms)
//   - httpGet/fetch/request: network I/O + non-determinism
//   - random/rand/uuid/now_unix_nano: non-deterministic
//   - glob/walk/ls: sandbox violation (recipes do not see the filesystem)
//   - eval/parse/compile: recursive template loading (same hole as {{ define }})
//   - regexpMatch/regexpReplace (on user input): ReDoS + value-leak (I2)
func helperFuncMap() template.FuncMap {
	return template.FuncMap{
		"groupBy":           templateGroupBy,
		"groupByWith":       templateGroupByWith,
		"groupByWithSorted": templateGroupByWithSorted,
		"legendFor":         templateLegendFor,
		"bucketName":        templateBucketName,
		"stripSuffix":       templateStripSuffix,
		"firstLabelOf":      templateFirstLabelOf,
	}
}

// templateGroupBy returns the comma-separated safeGroupLabels result for the
// current render context. Used in: sum by ({{ groupBy . }}) (...)
//
// Determinism: ctx.GroupBy is a sorted slice; same input → same output.
func templateGroupBy(ctx RenderContext) string {
	return strings.Join(ctx.GroupBy, ", ")
}

// templateGroupByWith returns the same comma-separated safe grouping as
// groupBy, but appends each extra label not already present (deduplicated,
// in argument order). Banned labels in extras are silently dropped (T8).
//
// Used in: sum by ({{ groupByWith . "le" }}) (...)
func templateGroupByWith(ctx RenderContext, extras ...string) string {
	seen := make(map[string]bool, len(ctx.GroupBy)+len(extras))
	result := make([]string, 0, len(ctx.GroupBy)+len(extras))
	for _, l := range ctx.GroupBy {
		seen[l] = true
		result = append(result, l)
	}
	for _, e := range extras {
		if bannedLabels[e] || seen[e] {
			continue // drop banned or duplicate labels (T8 defense-in-depth)
		}
		seen[e] = true
		result = append(result, e)
	}
	return strings.Join(result, ", ")
}

// templateGroupByWithSorted is like groupByWith, but re-sorts the merged
// label set alphabetically. Required for histogram_quantile templates that
// must match v0.1/v0.2 Go-recipe goldens byte-for-byte (those Go recipes
// inject "le" via ensureLabel, which sorts the full set after merging).
//
// Used in: sum by ({{ groupByWithSorted . "le" }}) (...)
func templateGroupByWithSorted(ctx RenderContext, extras ...string) string {
	seen := make(map[string]bool, len(ctx.GroupBy)+len(extras))
	merged := make([]string, 0, len(ctx.GroupBy)+len(extras))
	for _, l := range ctx.GroupBy {
		if seen[l] {
			continue
		}
		seen[l] = true
		merged = append(merged, l)
	}
	for _, e := range extras {
		if bannedLabels[e] || seen[e] {
			continue
		}
		seen[e] = true
		merged = append(merged, e)
	}
	sort.Strings(merged)
	return strings.Join(merged, ", ")
}

// templateLegendFor renders the Grafana legend template for this panel.
// Delegates to the existing legendFor func (helpers.go) over ctx.GroupBy.
// Returns "" when GroupBy is empty.
func templateLegendFor(ctx RenderContext) string {
	return legendFor(ctx.GroupBy)
}

// templateBucketName ensures a metric name ends with _bucket. Idempotent.
//
//	bucketName("http_request_duration_seconds")        → "http_request_duration_seconds_bucket"
//	bucketName("http_request_duration_seconds_bucket") → "http_request_duration_seconds_bucket"
func templateBucketName(s string) string {
	const suffix = "_bucket"
	if strings.HasSuffix(s, suffix) {
		return s
	}
	return s + suffix
}

// templateStripSuffix returns s with suffix removed if present. Idempotent.
//
//	stripSuffix("http_request_size_bytes_bucket", "_bucket") → "http_request_size_bytes"
//	stripSuffix("http_request_size_bytes",        "_bucket") → "http_request_size_bytes"
func templateStripSuffix(s, suffix string) string {
	return strings.TrimSuffix(s, suffix)
}

// templateFirstLabelOf returns the first candidate label name that is present
// on the matched metric, walking candidates in argument order. Returns "" if
// none of the candidates are present. Used by recipes that pick a label
// dynamically at render time, e.g. service_http_errors picks the first of
// {status_code, code}:
//
//	{{ .Metric }}{ {{ firstLabelOf . "status_code" "code" }}=~"5.." }
//
// Determinism: candidates are scanned in argument order; the result depends
// only on (candidates, ctx.Labels). The receiver is the closed Labels map
// (names-only, invariant I2).
func templateFirstLabelOf(ctx RenderContext, candidates ...string) string {
	for _, c := range candidates {
		if _, ok := ctx.Labels[c]; ok {
			return c
		}
	}
	return ""
}

// Parse parses src as a text/template with the closed FuncMap and
// Option("missingkey=error"). Validates:
//   - No forbidden directives ({{ define }}, {{ template }}, {{ block }})
//     → ErrForbiddenDirective
//   - AST node count ≤ maxASTNodes → ErrASTNodeBudgetExceeded
//
// Returns a ready-to-render Template on success.
func Parse(name, src string) (*Template, error) {
	tpl, err := template.New(name).
		Option("missingkey=error").
		Funcs(helperFuncMap()).
		Parse(src)
	if err != nil {
		return nil, fmt.Errorf("template parse %q: %w", name, err)
	}

	// {{ define }} and {{ block }} cause the parser to register additional
	// associated templates. More than one template in the set means one of
	// these forbidden directives was used.
	if len(tpl.Templates()) > 1 {
		return nil, fmt.Errorf("%w in %q", ErrForbiddenDirective, name)
	}

	// Walk the AST: reject {{ template }} nodes; enforce node-count budget.
	if err := validateAST(tpl.Tree, name); err != nil {
		return nil, err
	}

	return &Template{tpl: tpl}, nil
}

// MustParse is like Parse but panics on any error. Intended for use by recipe
// loaders, which surface parse failures as LoadError before registration.
func MustParse(name, src string) *Template {
	t, err := Parse(name, src)
	if err != nil {
		panic(fmt.Sprintf("MustParse %q: %v", name, err))
	}
	return t
}

// Render executes the template with ctx as the dot context and returns the
// rendered string. Returns ErrTemplateOutputTooLarge if the output exceeds
// maxTemplateOutputBytes (T6).
func (t *Template) Render(ctx RenderContext) (string, error) {
	var buf bytes.Buffer
	if err := t.tpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template render: %w", err)
	}
	if buf.Len() > maxTemplateOutputBytes {
		return "", ErrTemplateOutputTooLarge
	}
	return buf.String(), nil
}

// validateAST walks the parse tree, rejecting {{ template }} nodes
// (ErrForbiddenDirective) and verifying the total node count is ≤
// maxASTNodes (ErrASTNodeBudgetExceeded).
func validateAST(tree *parse.Tree, name string) error {
	if tree == nil || tree.Root == nil {
		return nil
	}
	count, err := walkNode(tree.Root)
	if err != nil {
		return fmt.Errorf("template %q: %w", name, err)
	}
	if count > maxASTNodes {
		return fmt.Errorf("%w in %q: %d nodes", ErrASTNodeBudgetExceeded, name, count)
	}
	return nil
}

// walkNode recursively counts parse nodes and returns ErrForbiddenDirective
// on {{ template }} nodes.
func walkNode(node parse.Node) (int, error) {
	if node == nil {
		return 0, nil
	}
	total := 1 // count this node

	switch n := node.(type) {
	case *parse.TemplateNode:
		// {{ template "x" . }} — forbidden (DSL §7.5, T5).
		return 0, ErrForbiddenDirective

	case *parse.ListNode:
		for _, child := range n.Nodes {
			c, err := walkNode(child)
			if err != nil {
				return 0, err
			}
			total += c
		}

	case *parse.ActionNode:
		if n.Pipe != nil {
			c, err := walkNode(n.Pipe)
			if err != nil {
				return 0, err
			}
			total += c
		}

	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			c, err := walkNode(cmd)
			if err != nil {
				return 0, err
			}
			total += c
		}

	case *parse.CommandNode:
		for _, arg := range n.Args {
			c, err := walkNode(arg)
			if err != nil {
				return 0, err
			}
			total += c
		}

	case *parse.IfNode:
		c, err := walkBranchNode(n.Pipe, n.List, n.ElseList)
		if err != nil {
			return 0, err
		}
		total += c

	case *parse.RangeNode:
		c, err := walkBranchNode(n.Pipe, n.List, n.ElseList)
		if err != nil {
			return 0, err
		}
		total += c

	case *parse.WithNode:
		c, err := walkBranchNode(n.Pipe, n.List, n.ElseList)
		if err != nil {
			return 0, err
		}
		total += c

	case *parse.ChainNode:
		c, err := walkNode(n.Node)
		if err != nil {
			return 0, err
		}
		total += c

	// Leaf nodes (no children to walk):
	// *parse.TextNode, *parse.IdentifierNode, *parse.FieldNode,
	// *parse.DotNode, *parse.NilNode, *parse.VariableNode,
	// *parse.StringNode, *parse.NumberNode, *parse.BoolNode.
	}

	return total, nil
}

// walkBranchNode walks the shared pipe+list+elseList structure common to
// if, range, and with nodes.
func walkBranchNode(pipe *parse.PipeNode, list, elseList *parse.ListNode) (int, error) {
	total := 0
	if pipe != nil {
		c, err := walkNode(pipe)
		if err != nil {
			return 0, err
		}
		total += c
	}
	if list != nil {
		c, err := walkNode(list)
		if err != nil {
			return 0, err
		}
		total += c
	}
	if elseList != nil {
		c, err := walkNode(elseList)
		if err != nil {
			return 0, err
		}
		total += c
	}
	return total, nil
}
