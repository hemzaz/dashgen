# DashGen — Recipe Authoring Contract (v0.3+)

Recipes are the deterministic functions that translate classified Prometheus
metrics into Grafana panels. Starting with v0.3, every built-in recipe is a
YAML file validated against a CUE schema and rendered by Go `text/template`.
The Go-per-recipe code that existed in v0.1–v0.2 is fully replaced; zero
`<recipe_name>.go` files remain in `internal/recipes/`.

This document is the **authoring contract** for v0.3+.

- Full wire-format schema: [`RECIPES-DSL.md`](RECIPES-DSL.md)
- CLI authoring tools: [`RECIPES-CLI.md`](RECIPES-CLI.md)
- Beginner walkthrough: [`RECIPES-USER-GUIDE.md`](RECIPES-USER-GUIDE.md)

---

## 1. Overview

Each recipe is a YAML file with the fields described below. At startup the
loader (`internal/recipes/loader.go`) reads every built-in YAML embedded
via `go:embed` (from `internal/recipes/data/`) and every user-supplied YAML
found in `--recipes-dir` paths or the XDG default
(`$XDG_CONFIG_HOME/dashgen/recipes/`, fallback `~/.config/dashgen/recipes/`).
Each file is parsed, unified against the CUE schema (`internal/recipes/schema.cue`),
and decoded into a `YAMLRecipe` struct implementing the standard `Recipe`
interface. Synth, validate, safety, and render never know whether a recipe
came from a YAML file or (historically) a Go struct.

---

## 2. Authoring contract

A recipe file **must**:

1. Have `apiVersion: dashgen.io/v1` and `kind: Recipe`.
2. Declare a unique `metadata.name` (snake_case).
3. Declare `metadata.section`, `metadata.profile`, and `metadata.confidence`.
4. Provide a `match` block that unambiguously identifies the metric(s) the
   recipe handles (see §4).
5. Provide at least one panel in the `panels` list with valid `query_template`
   and `title_template` (see §5).
6. Pass `dashgen recipe lint` with zero errors (see [`RECIPES-CLI.md`](RECIPES-CLI.md) §3.3).
7. Have a fixture entry in the relevant `*-realistic` fixture so end-to-end
   tests exercise it.
8. Have a discrimination case — at least one look-alike metric asserted absent
   from the generated `dashboard.json` — added to `TestDiscrimination_*`.

Code (or recipes) that ship without all of the above do not merge.

### 2.1 Confidence guidance

| Range | Meaning |
|-------|---------|
| 0.90–0.95 | Extremely specific match (exact metric name like `go_goroutines`, or unambiguous pair). |
| 0.80–0.89 | Strong label + name match (canonical HTTP request rate, DB pool pair). |
| 0.70–0.79 | Shape-based match (any histogram whose name says "duration" + has HTTP labels). |
| 0.60–0.69 | Probable match with known look-alike risk; only land when the discrimination test is sharp. |
| < 0.60 | Reserved for AI-enriched unknown-family grouping. |

Within a profile, higher confidence wins when the panel cap is reached.

### 2.2 Anti-patterns (never do these)

- Match on metric name alone without any label or type signal.
- Emit a query referencing a label the metric may not have (causes
  `empty_result` warnings on every run).
- Hard-code a specific label value (e.g., `job="checkout"`) in a generic recipe.
- Use `without()` in grouping sets.
- Emit more than one panel from a recipe per source metric (multi-query
  panels are fine; multi-panel-per-metric is not).

---

## 3. Built-in recipe inventory (47 recipes)

Built-in recipes live under `internal/recipes/data/`, grouped by profile:

```
internal/recipes/data/
├── service/   # 20 recipes
├── infra/     # 14 recipes
└── k8s/       # 13 recipes
```

### Service profile (20 recipes)

| Name | Section | Confidence | Primary signal |
|------|---------|-----------|----------------|
| `service_http_rate` | traffic | 0.85 | counter + `service_http` trait |
| `service_http_errors` | errors | 0.85 | counter + status label + HTTP trait |
| `service_http_latency` | latency | 0.85 | histogram + `service_http` + `latency_histogram` |
| `service_cpu` | cpu | 0.85 | `process_cpu_seconds_total` or `container_cpu_usage_seconds_total` |
| `service_memory` | memory | 0.85 | `process_resident_memory_bytes` or `container_memory_working_set_bytes` |
| `service_grpc_rate` | traffic | 0.85 | counter + `service_grpc` trait |
| `service_grpc_errors` | errors | 0.85 | `grpc_code != "OK"` filter |
| `service_grpc_latency` | latency | 0.85 | histogram + `service_grpc` + `latency_histogram` |
| `service_goroutines` | saturation | 0.90 | exact `go_goroutines` gauge, `max by (instance)` |
| `service_gc_pause` | latency | 0.85 | `go_gc_duration_seconds` summary-or-histogram |
| `service_db_pool_go_sql_stats` | saturation | 0.80 | `go_sql_stats_connections_in_use` / `_max` pair |
| `service_db_pool_pgxpool` | saturation | 0.80 | `pgxpool_acquired_connections` / `_max` pair |
| `service_db_query_latency` | latency | 0.80 | histogram + `latency_histogram` + name contains query/db/sql, NOT HTTP/gRPC |
| `service_tls_expiry` | saturation | 0.80 | gauge ending `_tls_not_after_timestamp` / `_cert_expiry_timestamp_seconds` |
| `service_cache_hits` | traffic | 0.80 | `*_cache_hits_total` + `*_cache_misses_total` pair |
| `service_client_http` | traffic | 0.75 | counter + name contains "client" + has status label |
| `service_job_success` | errors | 0.80 | `*_jobs_succeeded_total` + `*_jobs_failed_total` pair |
| `service_kafka_consumer_lag` | errors | 0.85 | `kafka_consumergroup_lag` / `kafka_consumergroup_lag_sum` gauge |
| `service_request_size` | saturation | 0.75 | histogram + name ends `_request_size_bytes` + HTTP-shape guard |
| `service_response_size` | saturation | 0.75 | histogram + name ends `_response_size_bytes` + HTTP-shape guard |

### Infra profile (14 recipes)

| Name | Section | Confidence | Primary signal |
|------|---------|-----------|----------------|
| `infra_cpu` | cpu | 0.85 | `node_cpu_seconds_total` mode breakdown |
| `infra_memory` | memory | 0.85 | `node_memory_Mem{Available,Total}_bytes` pair |
| `infra_disk` | disk | 0.85 | `node_filesystem_{avail,size}_bytes` pair |
| `infra_network_receive` | network | 0.85 | `node_network_receive_bytes_total` |
| `infra_network_transmit` | network | 0.85 | `node_network_transmit_bytes_total` |
| `infra_load` | cpu | 0.90 | `node_load{1,5,15}` gauges |
| `infra_filesystem_usage` | disk | 0.85 | used-ratio per `{instance, mountpoint, fstype}` |
| `infra_file_descriptors` | overview | 0.90 | `process_{open,max}_fds` ratio |
| `infra_nic_errors` | network | 0.85 | `node_network_*_{errs,drop}_total` counters |
| `infra_conntrack` | saturation | 0.90 | `node_nf_conntrack_entries{,_limit}` ratio |
| `infra_disk_iops` | disk | 0.85 | `node_disk_{reads,writes}_completed_total` |
| `infra_disk_io_latency` | disk | 0.85 | `node_disk_io_time_seconds_total` / weighted variant |
| `infra_ntp_offset` | overview | 0.90 | `node_timex_offset_seconds` |
| `infra_interrupts` | saturation | 0.80 | exact `node_interrupts_total` counter |

### Kubernetes profile (13 recipes)

| Name | Section | Confidence | Primary signal |
|------|---------|-----------|----------------|
| `k8s_pod_health` | pods | 0.90 | `kube_pod_status_phase` gauge |
| `k8s_container_cpu` | resources | 0.85 | cAdvisor `container_cpu_usage_seconds_total` with namespace/pod filter |
| `k8s_container_memory` | resources | 0.85 | cAdvisor `container_memory_working_set_bytes` with namespace/pod filter |
| `k8s_restarts` | workloads | 0.75 | `kube_pod_container_status_restarts_total` |
| `k8s_deployment_availability` | workloads | 0.90 | `kube_deployment_{spec,status_replicas_available}_replicas` pair |
| `k8s_node_conditions` | resources | 0.90 | 4-query fixed set over `kube_node_status_condition{condition=...}` |
| `k8s_pvc_usage` | resources | 0.85 | `kubelet_volume_stats_{available,capacity}_bytes` |
| `k8s_oom_kills` | pods | 0.90 | `kube_pod_container_status_terminated_reason{reason="OOMKilled"}` |
| `k8s_apiserver_latency` | resources | 0.90 | `apiserver_request_duration_seconds` histogram |
| `k8s_etcd_commit` | resources | 0.90 | `etcd_disk_backend_commit_duration_seconds` histogram |
| `k8s_hpa_scaling` | workloads | 0.90 | `kube_horizontalpodautoscaler_status_{current,desired}_replicas` pair |
| `k8s_coredns` | latency | 0.85 | `coredns_dns_request_duration_seconds` histogram + `coredns_dns_requests_total` counter |
| `k8s_scheduler_latency` | latency | 0.85 | `scheduler_scheduling_attempt_duration_seconds` histogram |

### Deliberate Tier-C splits (v0.3)

Three v0.2 single-recipe entries were split into two child recipes each
(commit `da62cc4`). The split was required because each pair of child recipes
has incompatible match predicates that cannot be unified in one YAML file
without general branching logic (ruled out by the non-goals in
[`RECIPES-DSL.md`](RECIPES-DSL.md)):

| v0.2 recipe | v0.3 children | Why split |
|---|---|---|
| `service_db_pool` | `service_db_pool_go_sql_stats`, `service_db_pool_pgxpool` | `go_sql_stats_*` and `pgxpool_*` are distinct metric families with different name patterns; a single `match` block cannot address both without OR logic. |
| `infra_network` | `infra_network_receive`, `infra_network_transmit` | Separate `_receive_` and `_transmit_` metric names; splitting enables per-direction panel layout and independent confidence tuning. |
| `k8s_container_resources` | `k8s_container_cpu`, `k8s_container_memory` | CPU and memory are independent metric families requiring distinct unit annotations (`cores` vs `bytes`) without conditional unit dispatch in the template. |

These splits change panel UIDs (because the recipe `Name()` changes). Goldens
were regenerated for the affected fixtures: `service-realistic`,
`infra-basic`, `infra-realistic`, `k8s-basic`, `k8s-realistic`.

---

## 4. YAML schema gist

The full field reference and CUE constraints live in [`RECIPES-DSL.md`](RECIPES-DSL.md).
The fields most authors touch:

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: <snake_case>          # required; unique across all loaded recipes
  section: <string>           # required; valid section for the declared profile
  profile: service|infra|k8s  # required
  confidence: <float>         # required; 0.0–1.0
  description: <string>       # one-line human description

match:                        # at least one discriminating field required
  type: counter|gauge|histogram|summary
  name_equals: <string>       # exact metric name
  name_contains: <string>     # substring match
  name_suffix: <string>       # suffix match
  any_trait: [<trait>, ...]   # any of these classifier traits must be present
  not_traits: [<trait>, ...]  # none of these may be present
  required_labels: [<label>, ...]

pair_with:                    # optional; enables multi-metric join panels
  suffix_swap:
    from_suffix: <string>
    to_suffix: <string>
  on_missing: omit|warn

panels:
  - title_template: <go-template>    # required
    kind: timeseries|stat|gauge      # required
    unit: <string>                   # required (reqps, bytes, s, percentunit …)
    query_template: <go-template>    # required
    legend_template: <go-template>
    rationale_template: <go-template>
    preferred_labels: [<label>, ...] # passed to groupBy helper
    requires_pair: true|false
```

Available template helpers: `groupBy`, `legendFor`, `firstLabelOf`, `.Window`,
`.Metric`, `.Profile`. Full helper reference in [`RECIPES-DSL.md`](RECIPES-DSL.md).

---

## 5. Worked example — Tier-A recipe

A Tier-A recipe matches on a single type + trait, with no conditional logic in
the query template. `service_http_rate` is the canonical example:

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_http_rate
  section: traffic
  profile: service
  confidence: 0.85
  description: "HTTP request rate by route/handler for counters carrying the service_http trait."
  tags: [http, traffic, counter]

match:
  type: counter
  any_trait: [service_http]

panels:
  - title_template: 'Request rate: {{ .Metric }}'
    kind: timeseries
    unit: reqps
    preferred_labels: [route, handler]
    query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}[{{ .Window }}]))'
    legend_template: '{{ legendFor . }}'
    rationale_template: 'Counter "{{ .Metric }}" with HTTP-shaped labels; rate over {{ .Window }} grouped by {{ groupBy . }}.'
```

`{{ groupBy . }}` calls `safeGroupLabels` under the hood: always includes
`job` and `instance` if present, appends `preferred_labels` that exist on the
metric descriptor, filters banned labels, and sorts for determinism.
`{{ .Window }}` resolves to the configured rate window (default `5m`).

---

## 6. Determinism and golden stability

Every YAML recipe produces the same output for the same inventory input. The
five-stage validate pipeline runs on every emitted query — recipes cannot
bypass safety. Panel UIDs are derived from `(dashboardUID, section,
metricName, kind)`, so they are stable across re-runs as long as the recipe
name and metric name stay the same.

Changing a recipe's `name` field breaks golden stability and requires
`UPDATE_GOLDENS=1 go test ./internal/app/generate/...`.

---

## 7. Test coverage

The YAML harness replaces the per-recipe Go test files that existed in
v0.1–v0.2. Every built-in recipe is exercised via parameterized tests driven
by `testdata/*.json` fixture tables in `internal/recipes/`:

| Test type | Asserts |
|---|---|
| `TestRecipeLoader` | Every YAML in `data/` loads without CUE validation errors. |
| `TestYAMLRecipe_Match` (table) | Match returns true for positive fixture metrics; false for named look-alikes. |
| `TestYAMLRecipe_BuildPanels` (table) | Expected panel count; queries pass `promql/parser.ParseExpr`; expected grouping labels present. |
| `TestGolden_<Profile><Class>` | Byte-identical `dashboard.json` + `rationale.md` + `warnings.json` vs `testdata/goldens/<fixture>/`. |
| `TestDeterminism_<Profile><Class>` | Two pipeline runs produce byte-identical output. |
| `TestDiscrimination_<Profile><Class>Realistic` | Named look-alike metrics are absent from `dashboard.json`. |

---

## 8. Adding a new recipe

1. Scaffold: `dashgen recipe scaffold --metric <name> --type <type> --section <section> --profile <profile> --output ~/.config/dashgen/recipes/<name>.yaml`
2. Edit the scaffolded file: tune `match`, `confidence`, `panels`.
3. Lint: `dashgen recipe lint ~/.config/dashgen/recipes/<name>.yaml`
4. Test: `dashgen recipe test ~/.config/dashgen/recipes/<name>.yaml --fixture testdata/fixtures/service-realistic`
5. To contribute built-in: move to `internal/recipes/data/<profile>/<name>.yaml`, add fixture entries, add look-alike negative assertion, regenerate goldens.

See [`RECIPES-USER-GUIDE.md`](RECIPES-USER-GUIDE.md) for a full worked walkthrough with copy-pasteable commands.
