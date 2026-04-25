package enrich

import (
	"fmt"
	"strings"
)

// ValidateBriefs is a defensive guard that asserts every MetricBrief in
// briefs carries label NAMES only — never label values or key=value pairs.
// It exists to enforce the V0.2-PLAN §2.5 redaction contract at the last
// step before any outbound HTTP call to a hosted enricher.
//
// The check is intentionally simple: a label entry that contains '=' is
// treated as a value-shaped leak (covers both `pod=checkout` and the
// quoted Prometheus matcher form `pod="checkout"`). Identifier-only
// strings — including those with underscores like `kube_pod_status_phase`
// or `request_id` — pass through.
//
// On the first offending entry the function returns a non-nil error
// naming the metric and the label string so the operator can trace the
// leak back to its caller. An empty slice is valid input and returns
// nil; this matches the noop enricher's "empty in, empty out" contract.
func ValidateBriefs(briefs []MetricBrief) error {
	for _, b := range briefs {
		for _, lbl := range b.Labels {
			if strings.ContainsRune(lbl, '=') {
				return fmt.Errorf(
					"enrich: metric %q has value-shaped label entry [%s]; MetricBrief.Labels must contain label names only (V0.2-PLAN §2.5)",
					b.Name, lbl,
				)
			}
		}
	}
	return nil
}
