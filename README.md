# DashGen

DashGen is an OSS CLI for cloud and platform engineers that inspects a single
Prometheus-compatible HTTP query endpoint and emits a first-pass, reviewable
Grafana dashboard as code. It discovers metrics, classifies them
deterministically, matches them against a closed set of recipes, validates
every candidate PromQL expression through a five-stage pipeline, and renders
three diff-friendly artifacts: Grafana dashboard JSON, reviewer-facing
rationale Markdown, and a machine-readable warnings summary.

## Non-goals

- Not a Grafana controller or reconciliation loop
- Not an alerting or SLO generator
- Not a daemon, operator, or multi-tenant service
- Not an AI-enriched product — every decision is deterministic
- Does not fabricate panels when confidence is low

## Install

DashGen uses a local Go module path (`module dashgen`), so clone and build:

```bash
git clone <repo-url> dashgen
cd dashgen
make build         # produces ./dashgen
```

## Usage

```bash
# Live backend
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --out ./dashboards

# Offline fixture (useful in CI / golden tests)
dashgen generate \
  --fixture-dir testdata/fixtures/service-basic \
  --profile service \
  --out /tmp/out
```

`--prom-url` and `--fixture-dir` are mutually exclusive; exactly one
must be provided.

## Flags

| Flag | Purpose |
|------|---------|
| `--prom-url` | Prometheus-compatible HTTP API base URL. |
| `--fixture-dir` | Offline fixture directory (metadata/series/instant JSON). |
| `--profile` | Dashboard profile: `service`, `infra`, or `k8s` (v0.1 ships `service`). |
| `--out` | Output directory for the three emitted files. |
| `--config` | YAML config file path (optional). |
| `--dry-run` | Render to stdout instead of writing files. |
| `--strict` | Treat any surviving warning as a failure (exit 4). |
| `--job` | Restrict discovery to a job label. |
| `--namespace` | Restrict discovery to a namespace label. |
| `--metric-match` | Metric-name substring filter. |

## Output

Each run emits three files into `--out`:

- `dashboard.json` — Grafana dashboard schema v39, datasource variable `$datasource`, stable UIDs.
- `rationale.md` — Reviewer-facing explanation of every included panel and every omission.
- `warnings.json` — Machine-readable summary of warnings and refusals, one entry per panel or query candidate, sorted by (section, panel_uid, code).

## Validation contract

Every candidate PromQL expression passes through five stages before it can be
emitted as a panel target. Each stage can accept, warn, or refuse; refusal is
the default for anything not understood.

1. **parse** — official PromQL parser
2. **selector** — label matcher sanity, banned-label rejection
3. **execute** — bounded instant query against the backend (per-query timeout, total-run budget)
4. **safety** — grouping denylist, cardinality heuristics
5. **verdict** — composition of prior stages into `accept`, `accept_with_warning`, or `refuse`

`--strict` upgrades any remaining warning to a failure before rendering.

## Safety policy

The following labels are refused as grouping or matcher targets unless the
user opts in through config:

- `request_id`
- `session_id`
- `trace_id`
- `user_id`

Aggregations that group beyond a small fixed set of scope labels
(`instance`, `job`, `namespace`, `pod`, `service`) trigger a
`high_cardinality_grouping` warning. Aggregations that run unscoped by any
of those keys trigger `unscoped_aggregation`.

## Determinism

Identical inputs must yield byte-identical outputs. Metric ordering, label
ordering, recipe tie-breaking, section ordering, panel ordering, warning
ordering, and stable ID inputs are all normalized. A golden test
(`TestGolden_ServiceBasic`) enforces this across the end-to-end pipeline.

## Development

Build, test, and static-analysis targets live in the Makefile:

```bash
make build         # compile ./cmd/dashgen
make test          # run the full suite with race detection
make tidy          # clean up go.mod/go.sum
make vet           # static analysis
make fmt           # gofmt -s -w
make fmt-check     # fail if any file needs formatting
make check         # fmt-check + vet + build + test
```

Regenerate golden outputs after an intentional change:

```bash
UPDATE_GOLDENS=1 go test ./internal/app/generate/
```

## Running against real Prometheus data

Helper scripts live in `scripts/` (not in the Makefile — deploy/build only).

```bash
# Run against the public Prometheus demo.
scripts/run-demo.sh /tmp/dashgen-demo

# Snapshot any live backend into a DashGen fixture directory.
# The fixture at testdata/fixtures/prometheus-demo/ is gitignored — regenerate it.
scripts/capture-prometheus.sh https://prometheus.demo.prometheus.io \
  testdata/fixtures/prometheus-demo

# Replay the captured fixture twice and assert byte-identical output.
scripts/replay-demo.sh
```

The capture script hashes each generated PromQL expression with SHA-256 and
stores the instant-query response under `instant/<hex[:16]>.json`, which is
the same lookup the `--fixture-dir` backend uses at replay time.

## License

MIT
