# DashGen Recipes — User Guide

This guide is for platform engineers and operators who want to use DashGen's
recipe system: either to generate dashboards from built-in recipes, or to
author custom recipes for their own metric families without modifying DashGen
itself.

For the full schema reference see [`RECIPES-DSL.md`](RECIPES-DSL.md).
For the CLI subcommand reference see [`RECIPES-CLI.md`](RECIPES-CLI.md).
For the authoring contract see [`RECIPES.md`](RECIPES.md).

---

## Prerequisites

- DashGen binary built from source (`make build`, produces `./dashgen`)
- Prometheus-compatible HTTP endpoint, or use offline fixtures for authoring

---

## Example 1 — Quickstart: generate dashboards from a live Prometheus

The simplest use of DashGen is pointing it at an existing Prometheus endpoint
and letting the 47 built-in recipes do the work.

```bash
# Generate a service dashboard from a live Prometheus
dashgen generate \
  --prom-url http://prometheus:9090 \
  --profile service \
  --out ./dashboards
```

DashGen will:
1. Discover every metric family exposed by the endpoint.
2. Classify each metric (traits: `service_http`, `service_grpc`, `latency_histogram`, etc.).
3. Match metrics against the 47 built-in recipes.
4. Validate every candidate PromQL expression through a five-stage pipeline.
5. Render three files into `./dashboards/`:
   - `dashboard.json` — Grafana dashboard, schema v39, stable UIDs, `$datasource` variable.
   - `rationale.md` — reviewer-facing explanation of every included panel and every omission.
   - `warnings.json` — machine-readable summary of warnings and refusals.

To import into Grafana:

```bash
curl -X POST http://grafana:3000/api/dashboards/import \
  -H 'Content-Type: application/json' \
  -d @dashboards/dashboard.json
```

To generate infra or Kubernetes dashboards, change `--profile`:

```bash
dashgen generate --prom-url http://prometheus:9090 --profile infra --out ./dashboards
dashgen generate --prom-url http://prometheus:9090 --profile k8s   --out ./dashboards
```

To scope discovery to a specific job or namespace:

```bash
# Only metrics with job="api-server"
dashgen generate --prom-url http://prometheus:9090 --profile service \
  --job api-server --out ./dashboards

# Only metrics with namespace="payments"
dashgen generate --prom-url http://prometheus:9090 --profile k8s \
  --namespace payments --out ./dashboards
```

### What to do with the output

- **`dashboard.json`**: import directly into Grafana via the UI or API. The
  dashboard has a `$datasource` variable wired to every panel — set it to your
  Prometheus data source name after import.
- **`rationale.md`**: read this to understand why each panel was included and
  which metrics were omitted (and why). This is the primary review artifact.
- **`warnings.json`**: machine-readable. `"verdict": "accept_with_warning"`
  entries are panels that fired but had non-fatal issues (empty result set,
  high cardinality grouping). `"verdict": "refuse"` entries are panels that
  were dropped.

---

## Example 2 — Authoring: create a custom recipe for your own exporter

Suppose you have a custom exporter that exposes a queue depth gauge:

```
mycorp_worker_queue_depth{worker="ingestion", instance="host1", job="worker"}
```

DashGen has no built-in recipe for `mycorp_worker_queue_depth`. Here is how
to author one, test it locally, and have DashGen use it without touching
DashGen's source code or rebuilding the binary.

### Step 1 — Initialize your user recipes directory

```bash
dashgen recipe init
```

This creates `~/.config/dashgen/recipes/` (respecting `$XDG_CONFIG_HOME`)
with a `README.md`, an `example.yaml`, and a `.gitignore`. Run this once.

To use a different directory:

```bash
dashgen recipe init --config-dir /etc/dashgen/recipes
```

### Step 2 — Scaffold a starter recipe

```bash
dashgen recipe scaffold \
  --metric mycorp_worker_queue_depth \
  --type gauge \
  --section saturation \
  --profile service \
  --name mycorp_queue_depth \
  --output ~/.config/dashgen/recipes/mycorp_queue_depth.yaml
```

The scaffolded file will look something like:

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: mycorp_queue_depth
  section: saturation
  profile: service
  confidence: 0.80
  description: "TODO: describe what operator question this panel answers."

match:
  type: gauge
  name_equals: mycorp_worker_queue_depth

panels:
  - title_template: 'Queue depth: {{ .Metric }}'
    kind: timeseries
    unit: short
    preferred_labels: [worker]
    query_template: 'max by ({{ groupBy . }}) ({{ .Metric }})'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'Gauge "{{ .Metric }}" — queue depth by worker.'
```

### Step 3 — Edit and tune the recipe

Open the file in your editor. Common adjustments:

- Change `unit` to match what the metric measures (`short` for counts,
  `bytes` for byte sizes, `s` for seconds, `percentunit` for 0.0–1.0 ratios).
- Adjust `preferred_labels` to include labels meaningful for your metric
  (e.g., `[worker, region]`).
- Refine `match` if the name alone is too broad — add `required_labels`:

```yaml
match:
  type: gauge
  name_equals: mycorp_worker_queue_depth
  required_labels: [worker]
```

- Set a meaningful `confidence` value. See [`RECIPES.md §2.1`](RECIPES.md)
  for the scale: `0.90+` for exact-name matches, `0.80–0.89` for
  label+name matches, `0.70–0.79` for shape-based matches.

### Step 4 — Lint the recipe

```bash
dashgen recipe lint ~/.config/dashgen/recipes/mycorp_queue_depth.yaml
```

Lint validates the recipe against the CUE schema and catches schema
violations, missing required fields, invalid template syntax, and regex
errors. Exits 0 if clean, 1 if there are errors.

```bash
# Lint multiple recipes at once
dashgen recipe lint ~/.config/dashgen/recipes/*.yaml

# Get JSON output for CI integration
dashgen recipe lint ~/.config/dashgen/recipes/mycorp_queue_depth.yaml \
  --output json
```

### Step 5 — Test the recipe against a fixture

`dashgen recipe test` runs your recipe against a local fixture directory
(no live Prometheus needed):

```bash
dashgen recipe test \
  ~/.config/dashgen/recipes/mycorp_queue_depth.yaml \
  --fixture testdata/fixtures/service-realistic
```

The output lists which metrics matched, what panels were produced, and what
PromQL was emitted. Use `--verbose` for step-by-step match predicate
evaluation:

```bash
dashgen recipe test \
  ~/.config/dashgen/recipes/mycorp_queue_depth.yaml \
  --fixture testdata/fixtures/service-realistic \
  --verbose
```

To test against a fixture that actually contains `mycorp_worker_queue_depth`,
capture a snapshot of your live Prometheus:

```bash
scripts/capture-prometheus.sh http://your-prometheus:9090 /tmp/mycorp-fixture
```

Then test against it:

```bash
dashgen recipe test \
  ~/.config/dashgen/recipes/mycorp_queue_depth.yaml \
  --fixture /tmp/mycorp-fixture
```

### Step 6 — Run dashgen generate with your custom recipe

Once the recipe lints and tests cleanly, it is automatically picked up by
`dashgen generate` from the XDG default location. No flags needed:

```bash
dashgen generate \
  --prom-url http://your-prometheus:9090 \
  --profile service \
  --out ./dashboards
```

Your `mycorp_queue_depth` recipe fires alongside the 47 built-in recipes.
Look for `mycorp_worker_queue_depth` panels in `dashboard.json` and the
matching rationale in `rationale.md`.

To use a non-default recipe directory, pass `--recipes-dir` (repeatable):

```bash
dashgen generate \
  --prom-url http://your-prometheus:9090 \
  --profile service \
  --recipes-dir /etc/dashgen/recipes \
  --recipes-dir /home/me/extra-recipes \
  --out ./dashboards
```

---

## Example 3 — Override: tweak a built-in recipe without forking DashGen

Suppose you want the `service_http_rate` recipe to group by your custom
`region` label in addition to `route` and `handler`. The built-in recipe
uses `preferred_labels: [route, handler]`. You can override it by dropping
a same-named YAML in your user recipes directory.

### Step 1 — Inspect the built-in recipe

```bash
dashgen recipe show service_http_rate
```

This prints the current built-in YAML after schema unification and defaults
are applied. Use this as a starting point for your override.

### Step 2 — Create your override file

```bash
dashgen recipe show service_http_rate > \
  ~/.config/dashgen/recipes/service_http_rate.yaml
```

Edit `~/.config/dashgen/recipes/service_http_rate.yaml`. Change
`preferred_labels` to add `region`:

```yaml
    preferred_labels: [region, route, handler]
```

### Step 3 — Verify the override is recognized

```bash
dashgen recipe list --source all
```

You will see `service_http_rate` listed twice — once as `builtin` and once
as `user`. DashGen prints a deterministic warning to stderr at load time:

```
WARN recipe "service_http_rate" overridden by user recipe at
     /home/me/.config/dashgen/recipes/service_http_rate.yaml
```

This warning is always emitted and cannot be suppressed — overrides are
never silent.

### Step 4 — Compare the two versions

```bash
dashgen recipe diff \
  ~/.config/dashgen/recipes/service_http_rate.yaml \
  --against-builtin \
  --fixture testdata/fixtures/service-realistic
```

`recipe diff` shows a side-by-side comparison of what panels the two versions
produce against the fixture. Verify that `region` appears in the emitted
PromQL grouping clause before running generate.

### Step 5 — Generate with the override active

```bash
dashgen generate \
  --prom-url http://your-prometheus:9090 \
  --profile service \
  --out ./dashboards
```

The generated dashboard uses your overridden `service_http_rate`. The
load-time override warning is printed to stderr on every run so the
override remains visible in logs.

---

## Quick reference

### Key commands

```bash
# Initialize user recipes directory
dashgen recipe init

# List all loaded recipes (built-in + user)
dashgen recipe list

# Show a specific recipe's resolved YAML
dashgen recipe show <name>

# Scaffold a new recipe file
dashgen recipe scaffold \
  --metric <metric> --type <type> \
  --section <section> --profile <profile> \
  --output <path>

# Lint one or more recipe files
dashgen recipe lint <file...>

# Test a recipe against a fixture
dashgen recipe test <file> --fixture <fixture-dir>

# Explain why a recipe matched (or didn't) for a specific metric
dashgen recipe explain \
  --name <recipe> --metric <metric> \
  --fixture <fixture-dir>

# Diff two recipes by their effect on a fixture
dashgen recipe diff <fileA> <fileB> --fixture <fixture-dir>

# Generate dashboards (user recipes loaded automatically)
dashgen generate \
  --prom-url <url> --profile <profile> --out <dir>

# Generate dashboards with an explicit extra recipe directory
dashgen generate \
  --prom-url <url> --profile <profile> \
  --recipes-dir <path> --out <dir>
```

### User recipe directory resolution

Directories are searched in this order:

1. Paths passed via `--recipes-dir` (repeatable; processed in order given).
2. `$XDG_CONFIG_HOME/dashgen/recipes/` if `XDG_CONFIG_HOME` is set.
3. `~/.config/dashgen/recipes/` as the fallback default.

A directory that does not exist is silently skipped. Run
`dashgen recipe init` to create and bootstrap the default directory.

### Override precedence

User recipes shadow built-ins with the same `name`. A load-time warning is
always emitted naming the overriding file. There is no way to suppress
this warning — it is intentional.

### Troubleshooting

**My recipe does not fire against a metric I expect:**

```bash
dashgen recipe explain \
  --name <recipe> --metric <metric> \
  --fixture <fixture-dir>
```

This walks every predicate in your `match` block and shows which one failed.

**Schema validation error I don't understand:**

```bash
dashgen recipe lint <file> --output json | jq .
```

Lint errors include `file:line:col` and a human-readable description. Raw CUE
constraint names never leak through to the user output.

**I want to know which recipes matched a given metric:**

```bash
dashgen inspect --prom-url <url> --profile service
```

The inspect report lists every metric, its classifier traits, and which recipe
matched it (or why none did).
