# `dashgen coverage`

`dashgen coverage --fixture-dir <dir>` reports which inventory metrics a
dashboard covers, which it doesn't, and how the uncovered metrics
cluster by family. It runs deterministically and offline; the
unknown-family clustering is **naive string-prefix** grouping (the AI
version lands in v0.2 Phase 5 behind `--enrich unknown-grouping`).

## Synopsis

```
# Inventory-only — every metric is "uncovered"
dashgen coverage --fixture-dir testdata/fixtures/service-realistic

# Inventory + dashboard — partition into covered / uncovered
dashgen coverage \
  --fixture-dir testdata/fixtures/service-realistic \
  --in testdata/goldens/service-realistic \
  --out coverage.json
```

`--fixture-dir` is required and must contain a `metadata.json` (the
Prometheus-shaped `map[name][]MetricMetadata` that `dashgen generate`
also consumes). `--in` is optional; when provided, it must contain a
`dashboard.json`.

Live-Prometheus discovery is **not** supported in v0.2; fixture-dir
is the only input mode.

## Output schema

```json
{
  "source_inventory": "testdata/fixtures/service-realistic/metadata.json",
  "source_dashboard": "testdata/goldens/service-realistic/dashboard.json",
  "summary": {
    "metrics_total": 35,
    "metrics_covered": 16,
    "metrics_uncovered": 19
  },
  "covered": ["api_http_requests_total", "go_goroutines", ...],
  "uncovered": ["...metrics not referenced by any panel..."],
  "unknown_families": [
    {"family": "kube",   "count": 5, "metrics": ["kube_pod_status_phase", ...]},
    {"family": "kafka",  "count": 2, "metrics": ["kafka_consumergroup_lag", ...]}
  ]
}
```

`covered` and `uncovered` are sorted lexically. `unknown_families` is
sorted by descending `count` (highest-impact uncovered family first),
ties broken by `family` lex.

When `--in` is omitted, `source_dashboard` is empty, every metric ends
up in `uncovered`, and `unknown_families` lists everything.

## How "covered" is computed

The orchestrator walks every non-row panel's `targets[].expr` in
`dashboard.json`, scans for identifiers, and intersects with the
inventory. A metric is "covered" when its name appears as an
identifier-boundary token in at least one expression. The match is
intentionally permissive — it catches the metric in `rate(<m>[5m])`,
in raw `<m>{job="x"}` selectors, and in the inner term of
`histogram_quantile(0.95, sum by (le) (rate(<m>[5m])))`.

False positives are possible (a metric name that appears as a label
value would also match — see `internal/coverage/report.go` for the
heuristic). They bias the count toward "covered", which is the right
direction for a coverage report: the goal is to surface unmapped
metrics, and a false positive only means "we didn't surface this one".

## Family grouping

Uncovered metrics are grouped by the prefix up to the first underscore:

| Metric                           | Family   |
|----------------------------------|----------|
| `http_requests_total`            | `http`   |
| `node_cpu_seconds_total`         | `node`   |
| `kube_pod_status_phase`          | `kube`   |
| `up`                             | `up`     |

This is heuristic — `kube_pod_*` and `kubelet_*` end up in different
families even though they share an exporter. Operators reading the
report can collapse families themselves; the deterministic grouping
just gives them a starting point.

The AI version of family grouping (v0.2 Phase 5) clusters by semantic
similarity instead of string prefix and proposes section names. It is
opt-in only behind `--enrich unknown-grouping`.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | report written successfully |
| `1`  | generic error |
| `8`  | input error — missing/malformed `metadata.json` or `dashboard.json` |
| `9`  | render error — could not write the JSON report |

`coverage` does **not** fail on uncovered metrics. The report is the
output; CI consumers decide what threshold (if any) is acceptable.

## Determinism

Two runs over identical input produce byte-identical JSON output.
This is asserted by `TestRun_DeterministicReport` in
[`internal/app/coverage/coverage_test.go`](../internal/app/coverage/coverage_test.go).

## Known limitations

- Inventory must come from a fixture-dir; live-Prometheus discovery
  is a v0.3 follow-up.
- Identifier-boundary matching does not parse PromQL, so a metric
  whose name happens to appear as a label value is counted as
  covered. Heuristic, biased toward false positives by design.
- Family grouping is one underscore deep. `kube_pod_*` and
  `kubelet_*` cluster separately.
