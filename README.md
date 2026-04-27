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
| `--in-place` | Skip rewriting unchanged output files (idempotent re-runs preserve mtime). |
| `--job` | Restrict discovery to a job label. |
| `--namespace` | Restrict discovery to a namespace label. |
| `--metric-match` | Metric-name substring filter. |

Enrichment flags (`--provider`, `--provider-model`, `--enrich`, `--cache-dir`,
`--no-enrich-cache`) are described in [`docs/AI-PROVIDERS.md`](docs/AI-PROVIDERS.md).
Quick example:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
dashgen generate --prom-url http://localhost:9090 --profile service \
    --provider anthropic --enrich titles,rationale
```

## Output

Each run emits three files into `--out`:

- `dashboard.json` — Grafana dashboard schema v39, datasource variable `$datasource`, stable UIDs.
- `rationale.md` — Reviewer-facing explanation of every included panel and every omission.
- `warnings.json` — Machine-readable summary of warnings and refusals, one entry per panel or query candidate, sorted by (section, panel_uid, code).

## Other commands

Beyond `generate`, the CLI ships four supporting subcommands:

```
dashgen validate ...   # validate one or more PromQL expressions against a backend
dashgen inspect ...    # diagnostic report: inventory + classification + recipe matches
dashgen lint ...       # audit an existing dashboard bundle for quality regressions
dashgen coverage ...   # report metrics covered vs uncovered by a dashboard bundle
```

`lint` and `coverage` are deterministic, offline, and run against an
existing bundle — useful in CI to catch drift after manual edits or
to surface unmapped metrics. See [`docs/lint.md`](docs/lint.md) for
the seven shipped check classes and [`docs/coverage.md`](docs/coverage.md)
for the report schema.

```
# Lint a generated dashboard
dashgen lint --in ./dashboards

# Coverage report against a fixture inventory + dashboard
dashgen coverage \
  --fixture-dir testdata/fixtures/service-realistic \
  --in testdata/goldens/service-realistic
```

## AI enrichment (optional)

Default `--provider off` is byte-identical to v0.1; AI is opt-in only and cannot
generate PromQL, upgrade a refused verdict, or bypass the validation pipeline.

```bash
# Anthropic (set ANTHROPIC_API_KEY first)
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider anthropic \
  --enrich titles,rationale \
  --out ./dashboards

# OpenAI (set OPENAI_API_KEY first)
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --provider openai \
  --enrich titles,rationale \
  --out ./dashboards
```

The first run contacts the provider's API; subsequent runs over the same inventory
are served from the on-disk cache with zero outbound traffic. Both providers
implement the same contract over the same prompt templates — switching between
`--provider anthropic` and `--provider openai` only changes title/rationale prose,
never query, verdict, or panel UID material. See
[`docs/AI-PROVIDERS.md`](docs/AI-PROVIDERS.md) for the full provider surface,
redaction contract, and extension guide.

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

## Roadmap

- **v0.1** (shipped): deterministic core, three profiles, three CLI
  commands, three-file output. Validated end-to-end against the public
  Prometheus demo + Grafana.
- **v0.2** (current): optional AI enrichment (titles + rationale) behind a
  `--provider` flag (`anthropic` and `openai` both live, shared redaction
  contract and on-disk cache), expanded recipe catalog (44 recipes across
  service / infra / k8s profiles), `dashgen lint` (offline bundle audit, seven
  check classes) and `dashgen coverage` (metrics coverage report). Detailed
  plan in [`docs/V0.2-PLAN.md`](docs/V0.2-PLAN.md); recipe catalog in
  [`docs/RECIPES.md`](docs/RECIPES.md). Stage definitions in
  [`docs/ROADMAP.md`](docs/ROADMAP.md).
- **v0.3** (planned): unknown-family metric grouping, user-extensible recipe
  catalog, and further enrichment improvements.

AI enrichment in v0.2 is strictly non-overriding: it cannot generate
PromQL, cannot upgrade a refused verdict, and cannot bypass the
validation pipeline. Every run with a populated cache is byte-identical
to the last.

## License

MIT
