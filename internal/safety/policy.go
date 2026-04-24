// Package safety owns risk policy: banned labels, risky grouping, and the
// refuse-vs-warn decision.
//
// The banned-label denylist is cheap to implement and is therefore real in
// the foundation; cardinality scoring and opt-in risky-mode logic land with
// the safety implementation agent.
package safety

import (
	"sort"
	"strings"

	"dashgen/internal/ir"
)

// bannedLabels is the canonical set of high-cardinality identifiers DashGen
// refuses to group by unless a user opts in. See PRODUCT_DOC.md and SPECS §9.
var bannedLabels = []string{
	"request_id",
	"session_id",
	"trace_id",
	"user_id",
}

// Policy evaluates safety for labels and groupings.
type Policy struct {
	extraDeny []string
}

// NewPolicy constructs a Policy with the built-in denylist, optionally
// extended by additional user-configured denylist entries.
func NewPolicy(extraDeny []string) *Policy {
	cp := append([]string(nil), extraDeny...)
	return &Policy{extraDeny: cp}
}

// BannedLabels returns the built-in denylist in sorted order.
//
// Determinism: the slice is returned sorted; callers may read it without
// re-sorting.
func (p *Policy) BannedLabels() []string {
	out := append([]string(nil), bannedLabels...)
	sort.Strings(out)
	return out
}

// EvaluateGrouping returns a Verdict for an attempted grouping by the given
// labels. Any label appearing in the denylist (or the policy's extra denies)
// causes a refusal; other groupings are accepted at the foundation level.
//
// Determinism: input is lowercased and checked against a sorted denylist.
func (p *Policy) EvaluateGrouping(labels []string) ir.Verdict {
	if p.IsBanned(labels) {
		return ir.VerdictRefuse
	}
	return ir.VerdictAccept
}

// IsBanned reports whether any of the supplied labels is on the effective
// denylist (built-in plus extra denies).
func (p *Policy) IsBanned(labels []string) bool {
	for _, l := range labels {
		if p.isDenied(l) {
			return true
		}
	}
	return false
}

func (p *Policy) isDenied(label string) bool {
	needle := strings.ToLower(label)
	for _, b := range bannedLabels {
		if b == needle {
			return true
		}
	}
	if p != nil {
		for _, b := range p.extraDeny {
			if strings.ToLower(b) == needle {
				return true
			}
		}
	}
	return false
}

// Warning codes emitted by CardinalityRisk. Exported so the validator can
// reference them without duplicating strings.
const (
	WarningHighCardinalityGrouping = "high_cardinality_grouping"
	WarningUnscopedAggregation     = "unscoped_aggregation"
)

// highCardinalityThreshold is the point at which a grouping dimension count
// is considered risky for a first-pass dashboard. See PRODUCT_DOC.md §7.
const highCardinalityThreshold = 4

// scopeLabels are the label keys that constrain a query to a reasonable
// slice of the fleet. If none appear in the selector scope, broad
// aggregations are flagged as unscoped.
var scopeLabels = []string{"instance", "job", "namespace", "pod", "service"}

// CardinalityRisk returns a deterministic warning code when the combination
// of a grouping set and the ambient selector scope is risky, or "" when the
// combination is safe.
//
// Rules:
//   - more than highCardinalityThreshold distinct grouping dimensions →
//     WarningHighCardinalityGrouping
//   - grouping present but selector scope has no job/namespace/instance →
//     WarningUnscopedAggregation
//
// Banned labels are handled separately by EvaluateGrouping; this function
// does not emit a warning for them.
func (p *Policy) CardinalityRisk(groupingLabels []string, selectorScope map[string]string) string {
	uniq := map[string]struct{}{}
	for _, l := range groupingLabels {
		l = strings.TrimSpace(strings.ToLower(l))
		if l == "" {
			continue
		}
		uniq[l] = struct{}{}
	}
	if len(uniq) > highCardinalityThreshold {
		return WarningHighCardinalityGrouping
	}
	if len(uniq) == 0 {
		return ""
	}
	// Grouping present; require at least one scope label with a non-empty
	// value in the selector.
	for _, k := range scopeLabels {
		if v, ok := selectorScope[k]; ok && strings.TrimSpace(v) != "" {
			return ""
		}
	}
	return WarningUnscopedAggregation
}
