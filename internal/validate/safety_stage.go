package validate

import (
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"dashgen/internal/safety"
)

// safetyOutcome mirrors executeOutcome: refusal short-circuits, warnings
// accumulate.
type safetyOutcome struct {
	refused  bool
	reason   string
	warnings []string
}

// evaluateSafety walks the AST, collects every explicit grouping set, and
// runs each through the safety policy. It also computes an ambient
// selector scope (label → value when a matcher fixes it) so the
// cardinality heuristic can spot unscoped aggregations.
//
// Precedence: a banned label anywhere in any grouping forces a refusal.
// Cardinality warnings are only emitted when no refusal was hit.
func evaluateSafety(root parser.Expr, policy *safety.Policy) safetyOutcome {
	scope := collectSelectorScope(root)
	groupings := collectGroupings(root)

	if policy != nil {
		for _, g := range groupings {
			if policy.IsBanned(g) {
				return safetyOutcome{
					refused: true,
					reason:  fmt.Sprintf("%s: %q", ReasonBannedLabelGrouping, firstBanned(g, policy)),
				}
			}
		}
	}

	seen := map[string]struct{}{}
	var warnings []string
	if policy != nil {
		for _, g := range groupings {
			code := policy.CardinalityRisk(g, scope)
			if code == "" {
				continue
			}
			if _, ok := seen[code]; ok {
				continue
			}
			seen[code] = struct{}{}
			warnings = append(warnings, code)
		}
	}
	return safetyOutcome{warnings: warnings}
}

// collectGroupings walks the AST and returns every explicit grouping set
// used by an AggregateExpr. `without` clauses are currently returned as-is;
// they still carry label names we want to screen against the denylist so
// a `without(user_id)` on a metric that otherwise grouped by user_id
// would not slip through.
func collectGroupings(root parser.Expr) [][]string {
	var out [][]string
	parser.Inspect(root, func(n parser.Node, _ []parser.Node) error {
		agg, ok := n.(*parser.AggregateExpr)
		if !ok {
			return nil
		}
		if len(agg.Grouping) == 0 {
			return nil
		}
		cp := make([]string, len(agg.Grouping))
		copy(cp, agg.Grouping)
		out = append(out, cp)
		return nil
	})
	return out
}

// collectSelectorScope returns the set of label=value constraints that
// appear as equality matchers on every vector selector. Only equality
// matchers with non-empty values count; regex and negation matchers are
// ignored because they do not reliably constrain cardinality.
//
// When multiple selectors disagree, the last-seen value wins; the
// cardinality heuristic only checks for presence of scope keys, so exact
// values do not matter.
func collectSelectorScope(root parser.Expr) map[string]string {
	scope := map[string]string{}
	parser.Inspect(root, func(n parser.Node, _ []parser.Node) error {
		vs, ok := n.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		for _, m := range vs.LabelMatchers {
			if m == nil {
				continue
			}
			if m.Name == "__name__" {
				continue
			}
			if m.Type != labels.MatchEqual {
				continue
			}
			if m.Value == "" {
				continue
			}
			scope[m.Name] = m.Value
		}
		return nil
	})
	return scope
}

func firstBanned(g []string, policy *safety.Policy) string {
	for _, l := range g {
		if policy.IsBanned([]string{l}) {
			return l
		}
	}
	return ""
}
