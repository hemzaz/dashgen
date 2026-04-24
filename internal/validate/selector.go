package validate

import (
	"fmt"

	"github.com/prometheus/prometheus/promql/parser"

	"dashgen/internal/safety"
)

// selectorErr is returned by checkSelectors. banned means the failure is
// due to a matcher on a banned label rather than a structural sanity bug.
type selectorErr struct {
	reason string
	banned bool
}

func (e *selectorErr) Error() string { return e.reason }

// checkSelectors walks the parsed expression and verifies that every vector
// selector either names a metric or has at least one non-empty label
// matcher, that all label names are valid identifiers, and that no matcher
// targets a safety-banned label.
func checkSelectors(root parser.Expr, policy *safety.Policy) *selectorErr {
	var firstErr *selectorErr
	parser.Inspect(root, func(n parser.Node, _ []parser.Node) error {
		vs, ok := n.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		if firstErr != nil {
			return nil
		}
		firstErr = checkVectorSelector(vs, policy)
		return nil
	})
	return firstErr
}

func checkVectorSelector(vs *parser.VectorSelector, policy *safety.Policy) *selectorErr {
	hasName := vs.Name != ""
	nonEmptyMatcher := false
	for _, m := range vs.LabelMatchers {
		if m == nil {
			continue
		}
		if m.Name == "" {
			return &selectorErr{reason: "matcher with empty label name"}
		}
		if !isValidIdentifier(m.Name) {
			return &selectorErr{reason: fmt.Sprintf("invalid label identifier %q", m.Name)}
		}
		// __name__ is the implicit metric-name matcher; don't count it as
		// an extra label-scoped matcher but let it count as the name.
		if m.Name == "__name__" {
			if m.Value != "" {
				hasName = true
			}
			continue
		}
		if policy != nil && policy.IsBanned([]string{m.Name}) {
			return &selectorErr{
				reason: fmt.Sprintf("matcher targets banned label %q", m.Name),
				banned: true,
			}
		}
		if m.Value != "" {
			nonEmptyMatcher = true
		}
	}
	if !hasName && !nonEmptyMatcher {
		return &selectorErr{reason: "selector has no metric name or non-empty label matcher"}
	}
	return nil
}

// isValidIdentifier reports whether s matches the PromQL identifier
// grammar: [a-zA-Z_][a-zA-Z0-9_]*. Empty strings are rejected.
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
