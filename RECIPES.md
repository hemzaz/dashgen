# DashGen — Recipe Catalog

Each recipe is a deterministic function from *classified metrics* to
*panels*. This file is the authoritative catalog of what recipes exist,
what signals they match, and what invariants each one must preserve.

Read `V0.2-PLAN.md` for the v0.2 strategy and phasing. Read
`ARCHITECTURE.md` for the overall pipeline shape.

---

## 1. Recipe authoring contract

Every recipe **must**:

1. Live in `internal/recipes/<recipe_name>.go` as a type implementing
   the `Recipe` interface (`Name`, `Section`, `Match`, `BuildPanels`).
2. Be registered under the correct profile registry:
   `recipes.NewServiceRegistry` / `NewInfraRegistry` / `NewK8sRegistry`.
3. Ship with these tests (minimum):
   - `TestX_Match`: table-driven positive **and** negative cases.
     Each confirmed false-positive class from the real world gets one
     negative row.
   - `TestX_BuildPanels`: verifies the generated PromQL is
     syntactically correct (parse-only, no live backend needed) and
     contains the expected aggregations / filters.
4. Contribute fixture entries to the `service-realistic` /
   `infra-realistic` / `k8s-realistic` fixtures (to be created
   alongside the existing `*-basic` fixtures) so end-to-end tests
   exercise it.
5. Ship with a **discrimination assertion** (either as a new case in
   `TestDiscrimination_*` or as a new `TestDiscrimination_X`) that
   names at least one look-alike metric the recipe **must not** match.
6. Document (as a doc comment at the top of the recipe file):
   - What operator question the panel is meant to answer.
   - The canonical signals (name patterns, label patterns, metadata).
   - The aggregation shape and why.
   - The confidence value and why it's set there.
   - Known look-alikes that must not match.

Code that ships without the full set does not merge.

### 1.1 Confidence values (guidance)

| Range | Meaning |
|-------|---------|
| 0.90 – 0.95 | Extremely specific match (exact metric name like `up`, or unambiguous family like `process_cpu_seconds_total`). |
| 0.80 – 0.89 | Strong label + name match (canonical HTTP request rate, Go runtime specifics). |
| 0.70 – 0.79 | Shape-based match (any histogram whose name says "duration" + has HTTP labels). Bulk of recipes live here. |
| 0.60 – 0.69 | Probable match with known look-alike risk; only land when the discrimination test is sharp. |
| < 0.60 | Reserve for AI-enriched unknown-family grouping in v0.2 Phase 5. |

Within a profile, higher confidence wins when the panel cap is hit
(see `internal/synth/enforcePanelCap`).

### 1.2 Anti-patterns (never do these)

- Matching on metric name alone without any label signal.
- Emitting a query that references a label the metric may not have
  (causes empty-result warnings on every run).
- Hardcoding a specific label *value* (e.g., `job="checkout"`) in a
  generic recipe.
- Using `without()` in grouping sets.
- Emitting more than one panel from a recipe per source metric
  (multi-query panels are fine; multi-panel-per-metric is not).

---

## 2. v0.1 baseline (shipped)

| Recipe | Profile | Section | Confidence | Match summary |
|--------|---------|---------|------------|---------------|
| `service_http_rate` | service | traffic | 0.85 | counter + `service_http` trait |
| `service_http_errors` | service | errors | 0.80 | counter + status-like label (`status_code` or `code`) |
| `service_http_latency` | service | latency | 0.75 | histogram + `latency_histogram` trait + `service_http` trait |
| `service_cpu` | service | saturation | 0.80 | counter ending `cpu_seconds_total` with `process`/`container` family |
| `service_memory` | service | saturation | 0.80 | gauge ending `memory_bytes` with `process`/`container` family |
| `infra_cpu` | infra | cpu | 0.80 | `node_cpu_seconds_total` specifically |
| `infra_memory` | infra | memory | 0.80 | `node_memory_Mem{Available,Total}_bytes` pair |
| `infra_disk` | infra | disk | 0.80 | `node_filesystem_{avail,size}_bytes` pair |
| `infra_network` | infra | network | 0.80 | `node_network_{receive,transmit}_bytes_total` |
| `k8s_container_resources` | k8s | resources | 0.80 | `container_{cpu,memory}_*` with `namespace`/`pod`/`container` labels |
| `k8s_pod_health` | k8s | pods | 0.80 | `kube_pod_status_phase` |
| `k8s_restarts` | k8s | workloads | 0.75 | `kube_pod_container_status_restarts_total` |

---

## 3. v0.2 Tier-1 recipes (must ship)

Total: 12 new recipes across profiles. Priority order within each
profile is top-down; earlier recipes depend on fewer new traits.

### 3.1 Service profile

#### 3.1.1 `service_grpc_rate` → traffic

- **Question**: how many RPC calls per second, per method?
- **Signals**:
  - Name matches `grpc_server_handled_total` (canonical) or
    `grpc_server_started_total`.
  - Labels include `grpc_method` **or** `method` + `grpc_service`.
  - New trait: `service_grpc` attached in classify when any of
    `grpc_method`, `grpc_service`, `grpc_type`, `grpc_code` are
    present.
- **Grouping**: `{instance, job, grpc_service, grpc_method}` minus any
  banned label.
- **Query**: `sum by (instance, job, grpc_service, grpc_method) (rate(grpc_server_handled_total[5m]))`
- **Confidence**: 0.85.
- **Look-alikes to reject**: counters whose name happens to start with
  `grpc_` but lack both `grpc_method` and `grpc_service` labels
  (e.g., internal metric of a gRPC client library counting retries).

#### 3.1.2 `service_grpc_errors` → errors

- **Question**: rate of non-OK RPC outcomes per method.
- **Signals**: same base metric as `service_grpc_rate` (need the
  same inventory) + label `grpc_code`.
- **Query**: `sum by (instance, job, grpc_service, grpc_method) (rate(grpc_server_handled_total{grpc_code!="OK"}[5m]))`
- **Confidence**: 0.85.
- **Look-alike to reject**: metrics with a bare `code` label that's not
  populated by `grpc_code` semantics (same risk class as the
  alertmanager `code` false positive fixed in v0.1).

#### 3.1.3 `service_grpc_latency` → latency

- **Question**: p50/p95/p99 RPC handling latency per method.
- **Signals**: histogram named `grpc_server_handling_seconds` (bare base
  name) + `service_grpc` trait + histogram type.
- **Grouping**: `{instance, job, grpc_service, grpc_method, le}`.
- **Query**: `histogram_quantile(Q, sum by (instance, job, grpc_service, grpc_method, le) (rate(grpc_server_handling_seconds_bucket[5m])))` for Q∈{0.50, 0.95, 0.99}.
- **Confidence**: 0.85.

#### 3.1.4 `service_goroutines` → saturation

- **Question**: is the process leaking goroutines?
- **Signals**: gauge `go_goroutines` exactly. This is one of the few
  recipes that matches on name equality because the Go runtime
  exporter is canonical.
- **Query**: `max by (instance, job) (go_goroutines)` — max rather
  than sum because each process reports its own count; summing across
  instances would misleadingly add.
- **Confidence**: 0.90.
- **Look-alikes**: none known. Skip the discrimination test here.

#### 3.1.5 `service_gc_pause` → saturation

- **Question**: how much time does the process spend in stop-the-world GC?
- **Signals**: histogram `go_gc_duration_seconds` **or**
  summary `go_gc_duration_seconds` (Go runtime exposes it as a summary
  on most versions — detect both). Summary handling is a new code path;
  recipes currently only emit histogram_quantile against histograms.
- **Query shape** (summary): `avg by (instance, job) (go_gc_duration_seconds{quantile="0.99"})`
- **Query shape** (histogram): p99 via histogram_quantile.
- **Confidence**: 0.85.

### 3.2 Infra profile

#### 3.2.1 `infra_load` → cpu

- **Question**: is the kernel's runqueue saturated?
- **Signals**: gauges `node_load1`, `node_load5`, `node_load15`. Emit
  a single panel per host with all three as separate query
  candidates.
- **Query**: `avg by (instance) (node_load1)` + two siblings for 5/15.
- **Confidence**: 0.90.

#### 3.2.2 `infra_filesystem_usage` → disk

- **Question**: what percent of each filesystem is in use?
- **Signals**: `node_filesystem_{avail,size}_bytes` pair + label
  `mountpoint`.
- **Query**:
  `(node_filesystem_size_bytes - node_filesystem_avail_bytes) / node_filesystem_size_bytes`
  grouped by `{instance, mountpoint, fstype}`.
- **Confidence**: 0.85.
- Note: overlaps conceptually with v0.1 `infra_disk` (which shows raw
  avail/size). The usage-ratio panel is a complementary, operator-ready
  saturation view. Keep both.

#### 3.2.3 `infra_file_descriptors` → saturation

- **Question**: are we close to the fd limit?
- **Signals**: `process_open_fds` and `process_max_fds` must both be
  present (gauge pair).
- **Query**: `process_open_fds / process_max_fds` grouped by `{instance, job}`.
- **Confidence**: 0.90.

#### 3.2.4 `infra_nic_errors` → network

- **Question**: any NIC errors or drops?
- **Signals**: `node_network_{receive,transmit}_{errs,drop}_total`
  (all four, if any present; emit what's available).
- **Query**: `sum by (instance, device) (rate(...[5m]))`, four
  series per device.
- **Confidence**: 0.85.

### 3.3 k8s profile

#### 3.3.1 `k8s_deployment_availability` → workloads

- **Question**: how many replicas are available vs. desired per deployment?
- **Signals**: pair `kube_deployment_status_replicas_available` +
  `kube_deployment_spec_replicas` (gauges).
- **Query**: emit both as side-by-side candidates; a third candidate
  for the ratio
  `kube_deployment_status_replicas_available / kube_deployment_spec_replicas`.
- **Confidence**: 0.90.

#### 3.3.2 `k8s_node_conditions` → resources

- **Question**: are any nodes not Ready or reporting memory/disk/pid
  pressure?
- **Signals**: `kube_node_status_condition`. Gauge valued 0/1.
- **Query**: `max by (node, condition) (kube_node_status_condition{status="true"})`
  for condition ∈ {`NotReady`, `MemoryPressure`, `DiskPressure`,
  `PIDPressure`}. One panel, one candidate per condition.
- **Confidence**: 0.90.

#### 3.3.3 `k8s_pvc_usage` → resources

- **Question**: are any persistent volumes running out of space?
- **Signals**: pair `kubelet_volume_stats_available_bytes` +
  `kubelet_volume_stats_capacity_bytes`.
- **Query**: `1 - (kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes)`
  grouped by `{namespace, persistentvolumeclaim}`.
- **Confidence**: 0.85.

#### 3.3.4 `k8s_oom_kills` → pods

- **Question**: are pods being OOMKilled?
- **Signals**: `kube_pod_container_status_terminated_reason` gauge
  filtered to `reason="OOMKilled"`.
- **Query**: `sum by (namespace, pod) (kube_pod_container_status_terminated_reason{reason="OOMKilled"})`
- **Confidence**: 0.90.

---

## 4. v0.2 Tier-2 recipes (best effort)

These ship if Tier-1 is done and time allows. They follow the same
authoring contract. Each is sketched here; full signals/queries get
worked out when the recipe is actually written.

### 4.1 Service profile

| Recipe | Section | Primary signal |
|--------|---------|----------------|
| `service_client_http` | traffic | outbound HTTP counter with `status_code` / `code` and either `url` or `host` label |
| `service_db_pool` | saturation | `*_sql_pool_*` or `pgxpool_*` gauges (max, idle, in_use) |
| `service_db_query_latency` | latency | histogram named `*_query_duration_seconds` with no HTTP labels (is **not** matched by `service_http_latency`) |
| `service_cache_hits` | traffic | counter pair `cache_{hits,misses}_total` or `*_cache_hits_total` |
| `service_job_success` | errors | counter pair `*_jobs_{success,failure}_total` or `*_jobs_completed_total{status=...}` |
| `service_tls_expiry` | saturation | gauge `*_tls_not_after_timestamp` / `*_cert_expiry_timestamp_seconds` minus `time()` |
| `service_request_size` | traffic | histogram `*_request_size_bytes` with HTTP labels |
| `service_response_size` | traffic | histogram `*_response_size_bytes` with HTTP labels |

### 4.2 Infra profile

| Recipe | Section | Primary signal |
|--------|---------|----------------|
| `infra_disk_io_latency` | disk | histogram `node_disk_io_time_seconds_total` + per-device rate |
| `infra_disk_iops` | disk | counter pair `node_disk_{reads,writes}_completed_total` |
| `infra_conntrack` | network | `node_nf_conntrack_entries` / `node_nf_conntrack_entries_limit` |
| `infra_ntp_offset` | overview | gauge `node_timex_offset_seconds` |
| `infra_interrupts` | cpu | counter `node_interrupts_total` or `node_vmstat_nr_irq_*` |

### 4.3 k8s profile

| Recipe | Section | Primary signal |
|--------|---------|----------------|
| `k8s_hpa_scaling` | workloads | `kube_horizontalpodautoscaler_status_*` |
| `k8s_apiserver_latency` | resources | `apiserver_request_duration_seconds` histogram |
| `k8s_etcd_commit` | resources | `etcd_disk_backend_commit_duration_seconds` histogram |
| `k8s_scheduler_latency` | resources | `scheduler_scheduling_attempt_duration_seconds` histogram |
| `k8s_coredns` | resources | `coredns_dns_request_duration_seconds` histogram |

---

## 5. v0.3+ Tier-3 candidates (deferred)

Not in scope for v0.2; listed so the catalog stays visible.

- JVM runtime family (jvm_memory, jvm_gc, jvm_threads, jvm_classes).
- Node.js runtime (`nodejs_*`).
- Python/Gunicorn runtime.
- Kafka broker / consumer lag (`kafka_consumergroup_lag`).
- RabbitMQ (`rabbitmq_queue_messages`).
- Redis-specific (`redis_commands_total`).
- NATS / NSQ.
- SMART disk health (`smartmon_*`).
- Power / thermal (`node_power_supply_*`, `node_hwmon_temp_celsius`).
- Nginx / Caddy / Envoy / HAProxy specific recipes (currently covered
  generically by `service_http_*`).
- CNI / eBPF networking plane.
- Gatekeeper / Kyverno policy metrics.

---

## 6. New classifier traits required by Tier-1 recipes

| Trait | Signal | Recipes that consume it |
|-------|--------|-------------------------|
| `service_grpc` | any of `grpc_method`, `grpc_service`, `grpc_type`, `grpc_code` labels | `service_grpc_rate`, `service_grpc_errors`, `service_grpc_latency` |
| `go_runtime` | metric name prefix `go_` with `instance`/`job` labels | `service_goroutines`, `service_gc_pause` |
| `node_exporter_present` | metric name prefix `node_` with `instance` label | guards `infra_*` recipes against matching cAdvisor-only backends |
| `kube_state_present` | metric name prefix `kube_` | guards `k8s_*` recipes (kube-state-metrics specifically, not cAdvisor) |

The two "present" traits are *guards*, not positive signals on
individual metrics. They're set once on the classified inventory, not
per-metric. Useful so `inspect` can explain why a recipe section is
empty — "kube-state-metrics not detected" beats silent omission.

---

## 7. Fixture requirements

v0.2 adds three new committed fixtures alongside the v0.1
`*-basic` + `service-realistic`:

### 7.1 `testdata/fixtures/service-realistic-v2`

Extends `service-realistic` with:

- gRPC server metrics (rate + errors + latency, positive matches).
- Go runtime metrics (`go_goroutines`, `go_gc_duration_seconds` summary).
- A look-alike gRPC client counter that lacks `grpc_method` — must
  NOT match `service_grpc_rate`.
- A DB query latency histogram with no HTTP label — must NOT match
  `service_http_latency` (already covered by v0.1's
  `queue_processing_duration_seconds` but reaffirmed with a DB-named
  metric).

### 7.2 `testdata/fixtures/infra-realistic`

Built from a canonical `node_exporter` metric subset. Must include:

- `node_load{1,5,15}` for `infra_load`.
- `node_filesystem_*` with multiple mountpoints (one near-full, one
  empty) for `infra_filesystem_usage`.
- `process_{open,max}_fds` pair for `infra_file_descriptors`.
- `node_network_*_{errs,drop}_total` for `infra_nic_errors`.
- A cAdvisor-style `container_*` metric that must NOT be picked up by
  `infra_*` recipes (discrimination guard).

### 7.3 `testdata/fixtures/k8s-realistic`

Built from a canonical kube-state-metrics subset. Must include:

- `kube_deployment_status_replicas_{available,unavailable}` pair plus
  `kube_deployment_spec_replicas`.
- `kube_node_status_condition` with one node in each of
  {`Ready=true`, `MemoryPressure=true`}.
- `kubelet_volume_stats_{available,capacity}_bytes` for one PVC.
- `kube_pod_container_status_terminated_reason{reason="OOMKilled"}`
  for one pod.
- A look-alike cAdvisor `container_memory_working_set_bytes` that must
  NOT accidentally be picked up by a kube recipe.

Each fixture ships with pre-recorded `instant/` responses for every
query the pipeline generates against it, so goldens are clean
(no empty_result noise).

---

## 8. Test matrix

For each recipe in §3 (Tier-1), the test suite runs:

| Test | Asserts |
|------|---------|
| `Test<Recipe>_Match` table | Match returns true for every positive case; false for every negative case (especially the look-alike classes §3 names). |
| `Test<Recipe>_BuildPanels` | Returns the expected number of panels; emits the expected number of query candidates per panel; query expressions pass `promql/parser.ParseExpr`. |
| `TestGolden_<Profile>Realistic` | Updated golden includes the new panel(s); diff is reviewable. |
| `TestDiscrimination_<Profile>Realistic` | The specific look-alike metrics from §3 are asserted absent from `dashboard.json`. |

CI runs `-race -cover ./...` as it does for v0.1. No recipe is allowed
to lower overall coverage.

---

## 9. How AI-assisted recipe authoring fits in (v0.2 Phase 5)

The goal of AI authoring support is to compress the loop
"I notice a new metric family → I write a recipe". It is **not** to
let AI emit recipes at runtime.

Workflow:

1. Engineer runs `dashgen inspect --prom-url ... --profile service
   --propose-recipes --provider anthropic` against a real backend.
2. `inspect` identifies metrics the classifier could not match.
3. The engineer hands each cluster to the provider with a prompt
   asking for (a) a recipe name, (b) signal description, (c) proposed
   query shape.
4. The provider returns a draft Go file.
5. The engineer reviews, edits, and commits. The committed code is
   plain deterministic Go that goes through the authoring contract in
   §1.

The provider never sees label values. Prompts include only metric
names, metadata (type, help, unit), and label names.

---

## 10. Forward-compatibility with enrichment

Recipes themselves are unaware of enrichment. The pipeline passes
`dashboard := synth.Synthesize(...)` to the enricher; the enricher
mutates title and rationale strings on panels but never the query
candidates.

If a future enrichment feature wants to influence query shape, that
change requires a new spec, not a new enricher. This is an explicit
wall to prevent creep.
