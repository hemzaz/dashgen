package validate

import (
	"errors"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

// parseExpr runs the Prometheus PromQL parser over expr. The returned
// parser.Expr is kept so later stages can walk the AST without re-parsing.
//
// Determinism: the official parser is used so parse outcomes track real
// Prometheus semantics rather than a bespoke heuristic. Its error messages
// are stable for a given parser version.
func parseExpr(expr string) (parser.Expr, error) {
	s := strings.TrimSpace(expr)
	if s == "" {
		return nil, errors.New("empty expression")
	}
	p := parser.NewParser(parser.Options{})
	return p.ParseExpr(s)
}
