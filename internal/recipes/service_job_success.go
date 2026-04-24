package recipes

// serviceJobSuccessRecipe answers the question: "What is the rate of
// successful vs. failed background jobs for a service?"
//
// Canonical signals:
//   - Counter pairs of the form <prefix>_jobs_succeeded_total and
//     <prefix>_jobs_failed_total, where <prefix> is any non-empty string.
//     Examples: worker_jobs_succeeded_total / worker_jobs_failed_total,
//               email_jobs_success_total / email_jobs_failure_total.
//   - Two naming variants are supported:
//       _jobs_succeeded_total → expects _jobs_failed_total
//       _jobs_success_total   → expects _jobs_failure_total
//
// Shape (B) — a single counter with a status label such as
// *_jobs_completed_total{status="success|failure"} — is NOT handled by this
// recipe. Shape (B) is a generalisation of the service_http_errors class
// (label-based status discrimination) and is covered by that recipe family.
// This recipe supports shape (A) only: the pair-counter pattern.
//
// Match strategy:
//   Match returns true for counters whose name ends with _jobs_succeeded_total
//   or _jobs_success_total. BuildPanels requires the corresponding failed
//   counter to be present in the inventory; without the pair no panel is
//   emitted.
//
// Aggregation shape:
//   sum by (<grouping>) — one series per grouping tuple.
//   <grouping> = safeGroupLabels(m, "queue", "handler") so that common
//   job-dispatching labels are included when present.
//   Two query candidates per panel:
//     1. success rate (cps)
//     2. failure rate (cps)
//
// Confidence: 0.80 — strong name-suffix match; pairs are commonly co-exported
// but the failed counter could be absent in unusual deployments, so not 0.90.
//
// Known look-alikes that must NOT match:
//   - *_jobs_failed_total / *_jobs_failure_total (keyed on success side only).
//   - *_jobs_completed_total{status=...} — shape (B), not handled here.
//   - Metrics ending in _jobs_succeeded but of gauge type.

import (
	"fmt"
	"sort"
	"strings"

	"dashgen/internal/inventory"
	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

const (
	jobSucceededSuffix = "_jobs_succeeded_total"
	jobSuccessSuffix   = "_jobs_success_total"
	jobFailedSuffix    = "_jobs_failed_total"
	jobFailureSuffix   = "_jobs_failure_total"
)

type serviceJobSuccessRecipe struct{}

// NewServiceJobSuccess returns the service_job_success recipe.
func NewServiceJobSuccess() Recipe { return &serviceJobSuccessRecipe{} }

func (serviceJobSuccessRecipe) Name() string    { return "service_job_success" }
func (serviceJobSuccessRecipe) Section() string { return "errors" }

// Match returns true for counters whose name ends with _jobs_succeeded_total
// or _jobs_success_total.
func (r serviceJobSuccessRecipe) Match(m ClassifiedMetricView) bool {
	if m.Type != inventory.MetricTypeCounter {
		return false
	}
	name := m.Descriptor.Name
	return strings.HasSuffix(name, jobSucceededSuffix) ||
		strings.HasSuffix(name, jobSuccessSuffix)
}

// BuildPanels emits one panel per matched success/failure pair found in the
// inventory. Returns nil when the profile is not ProfileService or when no
// complete pair is found.
func (r serviceJobSuccessRecipe) BuildPanels(inv ClassifiedInventorySnapshot, p profiles.Profile) []ir.Panel {
	if p != profiles.ProfileService {
		return nil
	}

	// Collect the set of failure-metric names present in the inventory.
	failSet := map[string]bool{}
	for _, m := range inv.Metrics {
		name := m.Descriptor.Name
		if strings.HasSuffix(name, jobFailedSuffix) || strings.HasSuffix(name, jobFailureSuffix) {
			failSet[name] = true
		}
	}

	type pair struct {
		prefix  string
		success string
		failure string
		metric  ClassifiedMetricView
	}
	seen := map[string]bool{}
	var pairs []pair

	for _, m := range inv.Metrics {
		name := m.Descriptor.Name
		var prefix, failName string
		switch {
		case strings.HasSuffix(name, jobSucceededSuffix):
			prefix = strings.TrimSuffix(name, jobSucceededSuffix)
			failName = prefix + jobFailedSuffix
		case strings.HasSuffix(name, jobSuccessSuffix):
			prefix = strings.TrimSuffix(name, jobSuccessSuffix)
			failName = prefix + jobFailureSuffix
		default:
			continue
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		if !failSet[failName] {
			continue
		}
		pairs = append(pairs, pair{
			prefix:  prefix,
			success: name,
			failure: failName,
			metric:  m,
		})
	}
	if len(pairs) == 0 {
		return nil
	}

	// Sort by prefix for deterministic panel ordering.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].prefix < pairs[j].prefix
	})

	panels := make([]ir.Panel, 0, len(pairs))
	for _, pr := range pairs {
		grouping := safeGroupLabels(pr.metric, "queue", "handler")
		by := strings.Join(grouping, ", ")

		successRate := fmt.Sprintf(
			"sum by (%s) (rate(%s[%s]))",
			by, pr.success, defaultRateWindow,
		)
		failureRate := fmt.Sprintf(
			"sum by (%s) (rate(%s[%s]))",
			by, pr.failure, defaultRateWindow,
		)

		panels = append(panels, ir.Panel{
			Title: fmt.Sprintf("Jobs success/fail rate: %s", pr.prefix),
			Kind:  ir.PanelKindTimeSeries,
			Unit:  "cps",
			Queries: []ir.QueryCandidate{
				{
					Expr:         successRate,
					LegendFormat: legendFor(grouping) + " success",
					Unit:         "cps",
				},
				{
					Expr:         failureRate,
					LegendFormat: legendFor(grouping) + " failure",
					Unit:         "cps",
				},
			},
			Confidence: 0.80,
			Rationale: fmt.Sprintf(
				"job success/failure pair %s / %s: rates per second per (%s).",
				pr.success, pr.failure, by,
			),
		})
	}
	return panels
}
