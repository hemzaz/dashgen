# DashGen Recipe DSL — v0.3 Specification

> Status: DRAFT (2026-04-27). Not yet implemented. Supersedes the
> "stay in Go" conclusion of `BIG_ROCKS.md` once the open questions
> in §16 resolve. Companion adversary spec:
> [`RECIPES-DSL-ADVERSARY.md`](RECIPES-DSL-ADVERSARY.md).

---

## 0. TL;DR

Move all 44 recipes out of Go and into a hybrid authoring layer:

- **Wire format:** YAML files, one recipe per file.
- **Schema:** declared in CUE (`internal/recipes/schema.cue`); validated at load time via `cuelang.org/go/cue`.
- **Rendering:** PromQL produced by Go `text/template` with a closed helper namespace bound at startup.
- **Loader:** YAML → CUE unification → typed Go struct → `Recipe` interface implementation. Both built-in and user-supplied recipes flow through the same loader; the `Recipe` interface is preserved unchanged so synth/validate/render/safety stay agnostic.
- **User extensibility:** `--recipes-dir <path>` and `$XDG_CONFIG_HOME/dashgen/recipes/*.yaml` load additional recipes at runtime. Zero rebuild. Zero Go toolchain on the user side.
- **Tier-C migration:** schema accepts a bounded set of join primitives (`pair_with: { suffix_swap, prefix_swap, explicit }`) that cover every multi-metric pattern in the existing catalog (filesystem usage, db pool, job success, cache hits, gc pause type-dispatch, k8s pairs). No "if this look at that" general logic; only the patterns that already exist.
- **Determinism:** preserved end-to-end. Same inventory + same recipe set ⇒ byte-identical output.

The strategic shift: BIG_ROCKS optimized for maintainer simplicity; this spec optimizes for user-population growth. Different objective functions; the v0.3 phase chooses the second.

---

## 1. Goals & Non-Goals

### Goals

| ID | Goal |
|----|------|
| G1 | A user with no Go toolchain can author a recipe by writing one YAML file and dropping it in a directory. |
| G2 | Every recipe currently in `internal/recipes/*.go` has a 1:1 YAML representation. The Go files are deleted after migration. |
| G3 | Schema violations surface at load time with positional errors (file:line:col + the constraint that failed). |
| G4 | The 5-stage validate pipeline still runs on every emitted query; recipes cannot bypass safety. |
| G5 | Determinism contract is preserved. Two runs against the same inventory + same recipe set produce byte-identical `dashboard.json`, `rationale.md`, `warnings.json`. |
| G6 | The schema is closed under documented growth policy: new schema features require a documented user case + design review. No reactive feature creep. |
| G7 | The template helper namespace is closed (no user-defined functions). Helpers are pure, deterministic, and audited. |
| G8 | User recipes can override built-in recipes by name, with a deterministic load-time warning naming the override. |

### Non-Goals

| ID | Non-Goal |
|----|----------|
| NG1 | No general-purpose scripting in user recipes. No Starlark, no Lua, no embedded Go. text/template is the rendering layer; CUE governs structure; nothing else is loaded. |
| NG2 | No runtime introspection of arbitrary metrics beyond what the existing `ClassifiedInventorySnapshot` exposes. |
| NG3 | No I/O from user recipes. Recipes are pure value transforms. |
| NG4 | No support for user-supplied helpers in `text/template.FuncMap`. The helper namespace is closed at compile time. |
| NG5 | No cross-recipe execution dependencies. Each recipe's `Match` and `BuildPanels` are evaluated in isolation. |
| NG6 | No replacement of the IR. Recipes still produce `[]ir.Panel` via the existing `Recipe` interface. |
| NG7 | No support for general-purpose YAML anchors / aliases across files. Each recipe file is self-contained. |
| NG8 | No CUE wire format for users. Maintainers write CUE schemas; users only ever see YAML. |

---

## 2. Architecture

```
┌─────────────────────┐
│  user recipes/      │  *.yaml — drop in, no rebuild
│  (XDG or --dir)     │
└──────────┬──────────┘
           │
┌──────────▼──────────┐
│  built-in recipes/  │  internal/recipes/data/*.yaml — shipped with binary, embedded via go:embed
└──────────┬──────────┘
           │
           │  yaml.v3 parse
           ▼
┌─────────────────────┐
│  YAML AST           │
└──────────┬──────────┘
           │  encoding/json marshal → cue.Context.CompileBytes
           ▼
┌─────────────────────┐    ┌──────────────────────────┐
│  CUE value          │ ◀──│  schema.cue (#Recipe)    │
└──────────┬──────────┘    └──────────────────────────┘
           │  cue.Value.Unify(schema)
           │  cue.Value.Validate(cue.Concrete(true))
           ▼
┌─────────────────────┐
│  validated CUE      │  errors here surface to user with file:line:col
└──────────┬──────────┘
           │  cue.Value.Decode(&recipe)
           ▼
┌─────────────────────┐
│  recipes.YAMLRecipe │  Go struct, implements recipes.Recipe
└──────────┬──────────┘
           │  recipes.Register(profile, &yamlRecipe{...})
           ▼
┌─────────────────────┐
│  recipes.Registry   │  same registry as today; synth doesn't know the difference
└──────────┬──────────┘
           │
┌──────────▼──────────┐
│  Match()            │  evaluates compiled #MatchPredicate against ClassifiedMetricView
└──────────┬──────────┘
           │
           │  on hit
           ▼
┌─────────────────────┐
│  BuildPanels()      │  expands query_template via text/template + helper FuncMap
└──────────┬──────────┘
           │
           ▼
   []ir.Panel  →  validate (5 stages)  →  render
```

### Package layout

```
internal/recipes/
  schema.cue                          # CUE schema — single source of truth
  schema_embed.go                     # //go:embed schema.cue → []byte
  loader.go                           # YAML → CUE → struct → Recipe
  loader_test.go
  template.go                         # text/template engine + FuncMap binding
  template_test.go
  helpers.go                          # safeGroupLabels, legendFor, etc. (existing)
  helpers_test.go
  matcher.go                          # MatchPredicate evaluator
  matcher_test.go
  pair.go                             # pair-resolution (suffix_swap, prefix_swap, explicit)
  pair_test.go
  yaml_recipe.go                      # struct that implements Recipe from a parsed YAML doc
  registry.go                         # registers built-in + user-loaded recipes
  errors.go                           # CUE error → user-readable error mapping

  data/                               # built-in recipes, shipped via go:embed
    service/
      service_http_rate.yaml
      service_http_errors.yaml
      ...
    infra/
      infra_cpu.yaml
      ...
    k8s/
      k8s_pod_health.yaml
      ...

  testdata/                           # corpus for loader tests
    valid/
      *.yaml
    invalid/
      *.yaml
    adversarial/                      # see RECIPES-DSL-ADVERSARY.md
      *.yaml
```

### What's deleted

After migration, `internal/recipes/*.go` keeps only:
- `schema.cue` + `schema_embed.go`
- `loader.go`, `template.go`, `helpers.go`, `matcher.go`, `pair.go`
- `yaml_recipe.go`, `registry.go`, `errors.go`
- The existing `types.go` (Recipe interface, ClassifiedMetricView, etc.) — unchanged.

The 44 `<recipe_name>.go` files and their `<recipe_name>_test.go` siblings are deleted. The per-recipe Match/BuildPanels assertions migrate to a shared parameterized test that loads `data/**/*.yaml` and asserts behavior against fixtures.

---

## 3. Recipe Lifecycle

```
discover  ──▶  parse YAML  ──▶  unify with schema  ──▶  decode to Go struct
                                                              │
                                                              ▼
                                                  recipes.Register(profile, recipe)
                                                              │
                              for each metric in inventory:   │
                                                              ▼
                                  ┌── matcher.Eval(predicate, ClassifiedMetricView)
                                  │        ├─ true  → continue
                                  │        └─ false → next metric
                                  ▼
                              ┌── pair.Resolve(spec, snapshot)         (only if pair_with present)
                              │        ├─ found    → bind .Pair into render context
                              │        └─ missing  → respect on_missing policy
                              ▼
                              ┌── template.Render(query_template, ctx)
                              │        → []string of PromQL expressions
                              ▼
                              ┌── template.Render(legend_template, ctx)
                              │        → []string of legend formats
                              ▼
                              build ir.Panel{ ... }
                              ▼
                          existing 5-stage validate runs per query
```

---

## 4. Schema (CUE)

The schema is the source of truth. Every YAML recipe is a value that must unify with `#Recipe`. Unification failures are the user-visible validation errors. Defaults declared in CUE apply if the field is absent.

### 4.1 Top-level recipe

```cue
package recipes

#Recipe: {
    apiVersion:  "dashgen.io/v1"
    kind:        "Recipe"

    // Identity
    name:        =~"^[a-z][a-z0-9_]*$" & strings.MaxRunes(64)
    section:     #Section
    profile:     "service" | "infra" | "k8s"
    confidence:  >=0.0 & <=1.0
    tier:        "v0.1" | "v0.2-T1" | "v0.2-T2" | "v0.2-T3" | "v0.3"

    // Optional pair-presence resolver
    pair_with?:  #PairSpec

    // Match predicate — what metrics this recipe fires on
    match:       #MatchPredicate

    // One or more panel templates emitted on a hit
    panels:      [...#PanelTemplate] & list.MinItems(1)

    // Optional documentation
    description?: string & strings.MaxRunes(280)
    tags?:       [...string]
}

#Section: "overview" | "traffic" | "errors" | "latency" |
          "saturation" | "cpu" | "memory" | "disk" | "network" |
          "pods" | "workloads" | "resources"
```

### 4.2 Match predicate

```cue
#MatchPredicate: #PrimitivePredicate | #LogicalPredicate

#PrimitivePredicate: {
    // metric type
    type?:               "counter" | "gauge" | "histogram" | "summary"

    // name shape — at most one of these may be set per primitive
    name_equals?:        string
    name_equals_any?:    [...string] & list.MinItems(1)
    name_has_prefix?:    string
    name_has_suffix?:    string
    name_contains?:      string
    name_contains_any?:  [...string] & list.MinItems(1)
    name_matches?:       =~"^.+$"  // valid Go regexp; loader compiles & validates

    // trait predicates
    any_trait?:          [...#TraitName] & list.MinItems(1)
    all_traits?:         [...#TraitName] & list.MinItems(1)
    none_trait?:         [...#TraitName] & list.MinItems(1)

    // label predicates
    has_label?:          string
    has_label_any?:      [...string] & list.MinItems(1)
    has_label_all?:      [...string] & list.MinItems(1)
    has_label_none?:     [...string] & list.MinItems(1)
}

#LogicalPredicate: {
    any_of?: [...#MatchPredicate] & list.MinItems(2)
    all_of?: [...#MatchPredicate] & list.MinItems(2)
    not?:    #MatchPredicate
}

#TraitName: "service_http" | "service_grpc" | "latency_histogram"
// Note: the trait set grows in lockstep with internal/classify.
// Adding a new trait requires updating the CUE schema in the same PR.
```

**Mutual-exclusion constraints.** A `#PrimitivePredicate` must declare AT MOST ONE name predicate (`name_equals`, `name_equals_any`, `name_has_prefix`, `name_has_suffix`, `name_contains`, `name_contains_any`, `name_matches`). The schema enforces this via an open constraint:

```cue
#PrimitivePredicate: {
    // ... fields above ...
    _name_count: len([
        for f in ["name_equals", "name_equals_any", "name_has_prefix",
                  "name_has_suffix", "name_contains", "name_contains_any",
                  "name_matches"] if f in self
    ])
    _name_count & <=1
}
```

(The exact CUE for the mutex is finalized during Phase 0; the constraint must be expressed and tested.)

### 4.3 Panel template

```cue
#PanelTemplate: {
    title_template:  string & strings.MaxRunes(160)
    kind:            "timeseries" | "stat" | "gauge" | "barchart" | *"timeseries"
    unit:            #Unit
    query_template:  string & strings.MaxRunes(2048)
    legend_template: string & strings.MaxRunes(160)

    // Optional explicit grouping. If omitted, helper safeGroupLabels()
    // is invoked at render time with the metric's natural label set.
    group_by?:       [...string]

    // Optional preferred labels for safeGroupLabels(). Merged with
    // {job, instance} (always included if present).
    preferred_labels?: [...string]

    // Optional override of the rate window. Default "5m".
    rate_window?:    =~"^[0-9]+(s|m|h|d)$"

    // Histogram-specific: which quantiles to emit when this panel uses histogram_quantile.
    quantiles?:      [...>=0.0 & <=1.0] & list.MinItems(1) & list.MaxItems(5)

    // Pair-dependent: this panel only emits if the pair was resolved successfully.
    requires_pair?:  bool | *false

    // Type-dispatch: this panel only emits when the matched metric is of this type.
    requires_metric_type?: "counter" | "gauge" | "histogram" | "summary"
}

#Unit: "ops/sec" | "errors/sec" | "seconds" | "bytes" | "bytes/sec" |
       "ratio" | "percent" | "short" | "iops" | "count" | "days" | string
// String fallback covers vendor-specific units; loader emits a warning
// for non-canonical units to encourage canonicalization over time.
```

### 4.4 Pair specification

This is the schema feature that lets Tier-C multi-metric recipes migrate. **Bounded by design**: only three modes (`suffix_swap`, `prefix_swap`, `explicit`). No general-purpose join.

```cue
#PairSpec: {
    // Exactly one of these three modes must be set.
    suffix_swap?: #SuffixSwap
    prefix_swap?: #PrefixSwap
    explicit?:    #ExplicitPair

    // What to do when the pair is missing from the inventory.
    on_missing:   "omit" | "warn" | "use_first_only" | *"omit"
}

#SuffixSwap: {
    // The matched metric name ends with `from_suffix`.
    // The pair candidate is the matched name with `from_suffix` replaced by `to_suffix`.
    // Example: matched node_filesystem_size_bytes (suffix _size_bytes),
    //          pair candidate node_filesystem_avail_bytes (suffix _avail_bytes).
    from_suffix:  string
    to_suffix:    string
}

#PrefixSwap: {
    // The matched metric name starts with `from_prefix`.
    // Pair candidate replaces `from_prefix` with `to_prefix`.
    from_prefix:  string
    to_prefix:    string
}

#ExplicitPair: {
    // Pair candidate is the literal metric name.
    name:         string
}
```

**Why these three and not more.** Walking the Tier-C catalog:

| Recipe | Pair shape | Resolver |
|---|---|---|
| `infra_filesystem_usage` | `_size_bytes` ↔ `_avail_bytes` | `suffix_swap` |
| `infra_disk` | same | `suffix_swap` |
| `infra_memory` | `MemTotal_bytes` ↔ `MemAvailable_bytes` | `explicit` |
| `infra_conntrack` | `_entries` ↔ `_entries_limit` | `explicit` |
| `infra_file_descriptors` | `process_open_fds` ↔ `process_max_fds` | `explicit` |
| `infra_network` | `_receive_bytes_total` ↔ `_transmit_bytes_total` | `suffix_swap` |
| `service_cache_hits` | `_cache_hits_total` ↔ `_cache_misses_total` | `suffix_swap` |
| `service_job_success` | `_jobs_succeeded_total` ↔ `_jobs_failed_total` | `suffix_swap` |
| `service_db_pool` | `go_sql_stats_*` OR `pgxpool_*` (two separate recipes, not one pair) | (n/a — split into two recipes) |
| `service_gc_pause` | summary OR histogram (type-dispatch, not pair) | (uses `panels` list with `requires_metric_type`) |
| `k8s_deployment_availability` | `_spec_replicas` ↔ `_status_replicas_available` | `suffix_swap` |
| `k8s_hpa_scaling` | `_current_replicas` ↔ `_desired_replicas` | `suffix_swap` |
| `k8s_pvc_usage` | `_capacity_bytes` ↔ `_available_bytes` | `suffix_swap` |
| `k8s_node_conditions` | one metric, four condition= filter values | (uses `panels` list, not pair) |
| `k8s_coredns` | histogram + counter (different shapes) | `explicit` |

Three resolvers cover every case. **No general-purpose join is introduced.**

### 4.5 Composition definitions

Common recipe shapes get reusable CUE definitions, reducing duplication and drift:

```cue
#HistogramQuantileRecipe: #Recipe & {
    match: type: "histogram"
    panels: [...{
        kind:       "timeseries"
        unit:       "seconds"
    }]
}

#PairRatioRecipe: #Recipe & {
    pair_with: _ | *{ on_missing: "omit" }
    panels: [{ requires_pair: true }, ...]
}

#NodeExporterRecipe: #Recipe & {
    profile: "infra"
    match:   any_of: [{ name_has_prefix: "node_" }]
    panels: [...{ preferred_labels: ["instance", "device", "mountpoint"] }]
}

#KubeStateRecipe: #Recipe & {
    profile: "k8s"
    match:   any_of: [{ name_has_prefix: "kube_" }]
    panels: [...{ preferred_labels: ["namespace", "pod"] }]
}
```

Concrete recipes unify with these where applicable. Built-in YAML recipes don't have to declare these compositions in the YAML — the schema does the unification.

---

## 5. Wire Format (YAML)

### 5.1 File layout

One recipe per file. Filename matches `name` field with `.yaml` suffix. Built-in recipes live under `internal/recipes/data/<profile>/<name>.yaml`. User recipes can live anywhere `--recipes-dir` points to (default `$XDG_CONFIG_HOME/dashgen/recipes/`).

Top-level YAML structure:

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: <recipe_name>
  section: <section>
  profile: <profile>
  confidence: <float>
  tier: <tier>
  description: <optional one-line>
  tags: [<optional tags>]

# (Optional) only for multi-metric recipes
pair_with:
  suffix_swap: { from_suffix: "...", to_suffix: "..." }
  on_missing: omit | warn | use_first_only

match:
  # See §6 — full match predicate language

panels:
  - title_template: "..."
    kind: timeseries
    unit: ops/sec
    query_template: |
      ...
    legend_template: "..."
    group_by: [...]
    preferred_labels: [...]
    rate_window: 5m
    quantiles: [0.5, 0.95, 0.99]
    requires_pair: false
    requires_metric_type: histogram
```

### 5.2 Worked examples

#### 5.2.1 Tier A — `service_http_rate`

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_http_rate
  section: traffic
  profile: service
  confidence: 0.85
  tier: v0.1
  description: "HTTP request rate by route + status code."
  tags: [http, traffic, counter]

match:
  type: counter
  any_trait: [service_http]

panels:
  - title_template: "HTTP request rate"
    kind: timeseries
    unit: ops/sec
    preferred_labels: [method, route, status_code]
    query_template: |
      sum by ({{ groupBy . }}) (
        rate({{ .Metric }}{ {{ .ScopeFilter }} }[{{ .Window }}])
      )
    legend_template: "{{ legendFor . }}"
```

#### 5.2.2 Tier B — `service_http_latency` (histogram quantile trio)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_http_latency
  section: latency
  profile: service
  confidence: 0.85
  tier: v0.1

match:
  all_traits: [service_http, latency_histogram]

panels:
  - title_template: "HTTP latency p{{ .Quantile100 }}"
    kind: timeseries
    unit: seconds
    quantiles: [0.5, 0.95, 0.99]
    preferred_labels: [method, route]
    query_template: |
      histogram_quantile({{ .Quantile }},
        sum by ({{ groupByWith . "le" }}) (
          rate({{ bucketName .Metric }}{ {{ .ScopeFilter }} }[{{ .Window }}])
        )
      )
    legend_template: "{{ legendFor . }}"
```

#### 5.2.3 Tier B — `service_request_size` (label predicate + suffix transform)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_request_size
  section: saturation
  profile: service
  confidence: 0.75
  tier: v0.2-T2

match:
  all_of:
    - type: histogram
    - any_of:
        - { name_has_suffix: "_request_size_bytes" }
        - { name_has_suffix: "_request_size_bytes_bucket" }
    - has_label_any: [method, handler]

panels:
  - title_template: "Request size p99"
    kind: timeseries
    unit: bytes
    quantiles: [0.99]
    query_template: |
      histogram_quantile(0.99,
        sum by ({{ groupByWith . "le" }}) (
          rate({{ bucketName (stripSuffix .Metric "_bucket") }}{ {{ .ScopeFilter }} }[{{ .Window }}])
        )
      )
    legend_template: "{{ legendFor . }}"
```

#### 5.2.4 Tier C — `infra_filesystem_usage` (pair + ratio)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: infra_filesystem_usage
  section: disk
  profile: infra
  confidence: 0.85
  tier: v0.2-T1

pair_with:
  suffix_swap: { from_suffix: "_size_bytes", to_suffix: "_avail_bytes" }
  on_missing: omit

match:
  all_of:
    - type: gauge
    - name_equals: node_filesystem_size_bytes

panels:
  - title_template: "Filesystem usage by mountpoint"
    kind: timeseries
    unit: ratio
    preferred_labels: [instance, mountpoint, fstype]
    requires_pair: true
    query_template: |
      ({{ .Metric }}{ {{ .ScopeFilter }} } - {{ .Pair.Name }}{ {{ .ScopeFilter }} })
      / {{ .Metric }}{ {{ .ScopeFilter }} }
    legend_template: "{{ legendFor . }} {{ index .Labels \"mountpoint\" }}"
```

#### 5.2.5 Tier C — `service_db_query_latency` (trait exclusion + name substring)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_db_query_latency
  section: latency
  profile: service
  confidence: 0.80
  tier: v0.2-T2

match:
  all_of:
    - type: histogram
    - any_trait: [latency_histogram]
    - name_contains_any: [query, db, sql]
    - none_trait: [service_http, service_grpc]

panels:
  - title_template: "DB query latency p99"
    kind: timeseries
    unit: seconds
    quantiles: [0.99]
    preferred_labels: [database, table, operation]
    query_template: |
      histogram_quantile(0.99,
        sum by ({{ groupByWith . "le" }}) (
          rate({{ bucketName .Metric }}{ {{ .ScopeFilter }} }[{{ .Window }}])
        )
      )
    legend_template: "{{ legendFor . }}"
```

#### 5.2.6 Tier C — `service_gc_pause` (type-dispatch)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_gc_pause
  section: latency
  profile: service
  confidence: 0.85
  tier: v0.2-T1

match:
  any_of:
    - { name_equals: "go_gc_duration_seconds", type: summary }
    - { name_equals: "go_gc_duration_seconds", type: histogram }

panels:
  - title_template: "GC pause p99 (summary)"
    kind: timeseries
    unit: seconds
    requires_metric_type: summary
    query_template: |
      max by ({{ groupByWith . "quantile" }}) (
        {{ .Metric }}{ quantile="0.99", {{ .ScopeFilter }} }
      )
    legend_template: "{{ legendFor . }}"

  - title_template: "GC pause p99 (histogram)"
    kind: timeseries
    unit: seconds
    requires_metric_type: histogram
    quantiles: [0.99]
    query_template: |
      histogram_quantile(0.99,
        sum by ({{ groupByWith . "le" }}) (
          rate({{ bucketName .Metric }}{ {{ .ScopeFilter }} }[{{ .Window }}])
        )
      )
    legend_template: "{{ legendFor . }}"
```

#### 5.2.7 Tier C — `k8s_node_conditions` (fixed query set)

```yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: k8s_node_conditions
  section: resources
  profile: k8s
  confidence: 0.90
  tier: v0.2-T1

match:
  type: gauge
  name_equals: kube_node_status_condition

panels:
  - title_template: "Node Ready"
    kind: stat
    unit: count
    query_template: |
      sum by (node) (
        {{ .Metric }}{ condition="Ready", status="true", {{ .ScopeFilter }} }
      )
    legend_template: "{{ legendFor . }}"

  - title_template: "Node DiskPressure"
    kind: stat
    unit: count
    query_template: |
      sum by (node) (
        {{ .Metric }}{ condition="DiskPressure", status="true", {{ .ScopeFilter }} }
      )
    legend_template: "{{ legendFor . }}"

  - title_template: "Node MemoryPressure"
    kind: stat
    unit: count
    query_template: |
      sum by (node) (
        {{ .Metric }}{ condition="MemoryPressure", status="true", {{ .ScopeFilter }} }
      )
    legend_template: "{{ legendFor . }}"

  - title_template: "Node PIDPressure"
    kind: stat
    unit: count
    query_template: |
      sum by (node) (
        {{ .Metric }}{ condition="PIDPressure", status="true", {{ .ScopeFilter }} }
      )
    legend_template: "{{ legendFor . }}"
```

---

## 6. Match Predicate Language

### 6.1 Grammar

```
predicate     := primitive | logical
primitive     := type? name_predicate? trait_predicates? label_predicates?
logical       := any_of | all_of | not
any_of        := { any_of: [predicate, predicate, ...] }   # 2+ items
all_of        := { all_of: [predicate, predicate, ...] }   # 2+ items
not           := { not: predicate }
type          := type: counter | gauge | histogram | summary
name_predicate := name_equals | name_equals_any | name_has_prefix |
                 name_has_suffix | name_contains | name_contains_any |
                 name_matches
trait_predicates := any_trait | all_traits | none_trait
label_predicates := has_label | has_label_any | has_label_all | has_label_none
```

A `primitive` may declare AT MOST ONE name predicate. The schema enforces this.

### 6.2 Combinators

| Combinator | Semantics |
|---|---|
| `any_of: [P1, P2, ...]` | Logical OR. True iff any inner predicate matches. Requires ≥2 items. |
| `all_of: [P1, P2, ...]` | Logical AND. True iff every inner predicate matches. Requires ≥2 items. |
| `not: P` | Logical NOT. True iff inner predicate does not match. |

A primitive predicate's fields are conjunctive: `{ type: counter, has_label: status_code }` is `type=counter AND has_label=status_code`.

### 6.3 Evaluation semantics

| Predicate | Evaluation |
|---|---|
| `type: T` | `metric.Type == T` |
| `name_equals: "foo"` | `metric.Name == "foo"` |
| `name_equals_any: [a, b]` | `metric.Name == a OR metric.Name == b` |
| `name_has_prefix: "foo_"` | `strings.HasPrefix(metric.Name, "foo_")` |
| `name_has_suffix: "_total"` | `strings.HasSuffix(metric.Name, "_total")` |
| `name_contains: "queue"` | `strings.Contains(metric.Name, "queue")` |
| `name_contains_any: [a, b]` | any substring matches |
| `name_matches: "^foo_.*"` | `regexp.MustCompile(...).MatchString(metric.Name)`. Loader compiles at load time and rejects invalid regex. |
| `any_trait: [T1, T2]` | `metric.HasTrait(T1) OR metric.HasTrait(T2)` |
| `all_traits: [T1, T2]` | `metric.HasTrait(T1) AND metric.HasTrait(T2)` |
| `none_trait: [T1]` | `NOT metric.HasTrait(T1)` |
| `has_label: "method"` | `metric.HasLabel("method")` |
| `has_label_any: [a, b]` | any label present |
| `has_label_all: [a, b]` | all labels present |
| `has_label_none: [a, b]` | no labels present |

Each metric in the inventory is evaluated against each registered recipe. Match evaluation is O(recipes × metrics × predicate-depth). For 50 recipes × 200 metrics × avg-depth 3 = ~30k evaluations, sub-millisecond on commodity hardware.

### 6.4 Forbidden constructs

The match predicate language deliberately does NOT include:
- Comparison operators on label values (e.g. `label_equals: { method: "POST" }`). User recipes filter at the matcher level only by label *names*. Label *values* never enter the matcher.
- Numeric comparisons on metric values. Recipes don't see metric values; they see classified inventory descriptors.
- Regex on label values. Same reason.
- References to other recipes / cross-recipe conditions.
- General-purpose Boolean expressions (e.g. embedded JS, embedded CEL). The grammar above is closed.

These exclusions are determinism-preserving and security-preserving (see ADVERSARY spec §1.3).

---

## 7. Template Engine

### 7.1 Runtime

Go `text/template` (stdlib). No `html/template` (escaping rules don't apply to PromQL). Each panel's `query_template`, `legend_template`, and `title_template` is parsed once at recipe load time, cached on the YAMLRecipe struct, and rendered per-match at synth time.

### 7.2 Variables in scope

The template's dot context is a `RenderContext` struct:

```go
type RenderContext struct {
    Metric        string             // matched metric name
    Type          string             // metric type
    Labels        map[string]string  // label names from descriptor (no values here, names only)
    LabelList     []string           // sorted label names
    ScopeFilter   string             // pre-rendered scope filter (e.g. `job="$job", instance="$instance"`)
    Window        string             // rate window (default "5m"; overrideable via panel.rate_window)
    GroupBy       []string           // result of safeGroupLabels(...) for this panel
    PreferredLabels []string         // from panel.preferred_labels
    Quantile      string             // current quantile (e.g. "0.99") when iterating
    Quantile100   string             // current quantile × 100, integer-formatted (e.g. "99")
    Pair          *PairContext       // present iff pair_with declared and pair was resolved
}

type PairContext struct {
    Name   string
    Type   string
    Labels map[string]string
}
```

**`Labels` carries only label NAMES, not values.** The `map[string]string` shape is preserved for compatibility with existing helpers; values are always empty strings or sorted-stable placeholders. **No template can leak label values.** (See ADVERSARY §1.5.)

### 7.3 Helper namespace

The `text/template.FuncMap` is bound at startup with a closed set of helpers. User recipes invoke these via the standard template syntax `{{ helperName arg1 arg2 }}` or pipeline form `{{ . | helperName }}`. **No user-defined helpers.** **No reflection.** **No `text/template`'s `{{ define }}` or `{{ template }}` directives** (parser is configured to reject them).

| Helper | Signature | Semantics |
|---|---|---|
| `groupBy` | `(ctx) → string` | Renders the comma-separated `safeGroupLabels` result for the current render context. |
| `groupByWith` | `(ctx, label1, label2, ...) → string` | Same as `groupBy` but ensures the additional labels are present (used for `le` in histogram_quantile, `quantile` in summary). |
| `legendFor` | `(ctx) → string` | Renders `"{{job}} {{instance}} ..."` legend pattern from the safe label set. |
| `bucketName` | `(metricName) → string` | Appends `_bucket` if the name doesn't already end with it. Idempotent. |
| `stripSuffix` | `(s, suffix) → string` | Returns `s` minus `suffix` if it ends with `suffix`, else `s`. |
| `appendSuffix` | `(s, suffix) → string` | Returns `s + suffix` if it doesn't already end with `suffix`. |
| `firstLabelOf` | `(ctx, label1, label2, ...) → string` | Returns the first label name from the list that is present in `ctx.Labels`, or empty. Used for status_code/code fallback. |
| `defaultRateWindow` | `() → string` | Returns the project's default rate window ("5m"). |
| `joinLabels` | `(labels) → string` | `,`-joins a label list. |
| `quantile` | `() → string` | Current quantile in render iteration. Equivalent to `.Quantile`. |
| `now` | n/a | **Not provided.** Determinism: no clock access. |
| `env` | n/a | **Not provided.** Hermetic: no env access. |
| `exec` | n/a | **Not provided.** No shell-out. |

The helper namespace is part of the schema's stability surface. Adding a helper is a v0.x bump (minor); removing or changing semantics is v0.(x+1) breaking.

### 7.4 Determinism rules for templates

- All map iterations in `text/template` are sorted by key (stdlib behavior, documented).
- All helpers are pure (no I/O, no time, no env, no random).
- Template output is bytes; trailing whitespace is trimmed by the loader before passing to the validate pipeline.
- Multi-line templates use YAML's `|` block scalar to preserve newlines. The PromQL parser is whitespace-insensitive within a token but not across tokens; recipes test that whitespace doesn't change verdict.

### 7.5 Forbidden template constructs

The loader rejects any recipe whose template contains:

- `{{ define }}` or `{{ template }}` (cross-template references)
- `{{ block }}` (subtemplate inheritance)
- Calls to undefined helpers (template parse error at load)
- References to undefined dot-context fields (template parse error at load)

These restrictions are enforced by:
1. Wrapping `text/template.New()` with `Option("missingkey=error")` so missing fields error rather than silently rendering "<no value>".
2. Walking the parsed template AST after parse, rejecting `*parse.TemplateNode` and similar.

---

## 8. Multi-Metric Joins (Tier-C)

### 8.1 Pair resolution algorithm

When `pair_with` is declared, on each `Match` hit the loader calls `pair.Resolve(spec, snapshot, matchedMetric)`:

```
function Resolve(spec, snapshot, matched):
    candidate_name = computeCandidateName(spec, matched.Name)
    if candidate_name == "":
        return nil, ErrNoCandidate
    pair = snapshot.LookupByName(candidate_name)
    if pair == nil:
        return nil, ErrPairMissing
    return PairContext{
        Name:   candidate_name,
        Type:   pair.Type,
        Labels: pair.LabelNames,   // names only, never values
    }, nil

function computeCandidateName(spec, matched_name):
    if spec.suffix_swap:
        if !strings.HasSuffix(matched_name, spec.suffix_swap.from_suffix):
            return ""
        return strings.TrimSuffix(matched_name, spec.suffix_swap.from_suffix) + spec.suffix_swap.to_suffix
    if spec.prefix_swap:
        if !strings.HasPrefix(matched_name, spec.prefix_swap.from_prefix):
            return ""
        return spec.prefix_swap.to_prefix + strings.TrimPrefix(matched_name, spec.prefix_swap.from_prefix)
    if spec.explicit:
        return spec.explicit.name
```

### 8.2 on_missing semantics

| Value | Behavior |
|---|---|
| `omit` (default) | Recipe doesn't fire. No panels emitted. |
| `warn` | Recipe fires; panels marked `requires_pair: true` emit a refused candidate with `WarningPairMissing`. Other panels emit normally. |
| `use_first_only` | Recipe fires; pair-dependent panels degrade to single-metric form via the helper `pairOrSelf` (returns matched metric if pair missing). Used rarely; opt-in. |

The default `omit` matches existing recipe behavior (graceful degradation for incomplete pairs).

### 8.3 Multiple pairs per recipe

A recipe declares one `pair_with` block. If multi-pair is needed (e.g. `service_db_pool` matches both `go_sql_stats_*` and `pgxpool_*`), the recipe is split into two recipes (`service_db_pool_go_sql_stats` and `service_db_pool_pgxpool`). This is a deliberate constraint: multi-pair logic is the start of the slope to a query language. Splitting is the design.

---

## 9. Loader

### 9.1 Discovery

```
discover(builtinFS, userDirs []string) []RecipeSpec:
    specs = []
    for path in walkBuiltin(builtinFS, "data/**/*.yaml"):
        specs.append(loadFile(path, source="builtin"))
    for dir in userDirs:
        for path in walkUser(dir, "**/*.yaml") sorted by path:
            specs.append(loadFile(path, source="user"))
    return specs

walkUser:
    follow symlinks within dir; reject symlinks pointing outside dir
    skip dotfiles (.*.yaml, ._*.yaml)
    enforce file size cap (default 64KB; configurable)
    enforce file count cap per dir (default 1024; configurable)
    deterministic sort by full path
```

Discovery is deterministic: same FS state ⇒ same load order. Built-in recipes always load first; user recipes load after, with override semantics (§10).

### 9.2 Validation pipeline

Per file:

```
1. yaml.v3 Unmarshal → generic map[string]interface{}
2. encoding/json Marshal → []byte (CUE consumes JSON, not YAML directly)
3. cue.Context.CompileBytes(jsonBytes) → cue.Value
4. cue.Value.Unify(schema) → cue.Value
5. cue.Value.Validate(cue.Concrete(true), cue.All()) → []error (with positions)
6. Decode → typed Go struct
7. Compile each template (query, legend, title) → cached *template.Template
8. Compile any name_matches regex → cached *regexp.Regexp
9. Validate the helper namespace coverage (every helper called by templates is declared in FuncMap)
10. Construct YAMLRecipe{...} and return
```

Any error in 1-9 surfaces with the source path and position. The error is wrapped as `recipes.ErrLoad` with a struct holding `(source, file, line, column, constraint, message)`.

### 9.3 Error mapping

CUE's diagnostics are positional but its messages can be cryptic. The loader wraps them with user-friendly translations. Mappings are tested in `loader_test.go` as a corpus of (input YAML, expected error message) pairs.

| CUE error class | User-visible message template |
|---|---|
| `incomplete value` (required field missing) | `recipes/<file>:<line>: missing required field '<field>'` |
| `conflicting values` (type mismatch) | `recipes/<file>:<line>: field '<field>' must be <expected-type>, got <actual>` |
| `failed list element X disjunction` | `recipes/<file>:<line>: section must be one of [<allowed>]; got '<actual>'` |
| `list element X is conflict` | similar to above, with the constraint name |
| `regexp` parse error | `recipes/<file>:<line>: name_matches '<pattern>' is not a valid Go regular expression: <err>` |
| Bottom from disjunction with default | falls back to default; no error surfaced |

### 9.4 Registration

After successful load:

```
for spec in specs:
    profile_registry := registry.For(spec.Profile)
    if existing := profile_registry.ByName(spec.Name); existing != nil:
        if existing.Source == "builtin" && spec.Source == "user":
            log.Warnf("user recipe '%s' overrides builtin", spec.Name)
            profile_registry.Replace(spec.Name, spec)
        else if existing.Source == spec.Source:
            return ErrDuplicateRecipe{name, paths}
        else if existing.Source == "user" && spec.Source == "builtin":
            // builtin loaded first by §9.1; this branch impossible
            unreachable()
    else:
        profile_registry.Add(spec)
sort each profile_registry by Name (deterministic tie-break preserved)
```

---

## 10. Registry & Precedence

| Source | Load order | Override semantics |
|---|---|---|
| Built-in (`internal/recipes/data/<profile>/*.yaml` via `go:embed`) | First | Cannot be removed; can be shadowed by user with the same name. |
| User (`--recipes-dir`) | Second | Overrides builtin by name; collisions among user recipes (same name) error at load. |
| Cross-profile collisions | n/a | Same name in different profiles is allowed; profile is part of identity. |

Override is logged at WARN level with both file paths so the user can debug "why did my recipe not fire?" by reading the log.

---

## 11. CLI Surface

### 11.1 New flags on `dashgen generate`

```
--recipes-dir <path>      Additional directory of user recipes (default: $XDG_CONFIG_HOME/dashgen/recipes/).
                          Repeatable; later wins on name collision.
--no-user-recipes         Ignore --recipes-dir and XDG default; load builtins only.
```

### 11.2 New subcommand: `dashgen recipes`

```
dashgen recipe list [--profile P] [--source builtin|user|all]
                Print all registered recipes (after load) with name, profile,
                section, confidence, source path. Deterministic sort.

dashgen recipe lint <file...>
                Validate one or more YAML files against the schema WITHOUT
                running generate. Reports schema errors with positions.
                Exit code 0 = all valid, 1 = any invalid.

dashgen recipe scaffold
                --metric <name>
                --type {counter,gauge,histogram,summary}
                --section <section>
                --profile {service,infra,k8s}
                [--output <path>]
                Emits a starter YAML file at <output> (default: stdout).

dashgen recipe show <name>
                Prints the resolved YAML for a registered recipe (after
                schema unification + defaults). Useful for "what did
                my recipe end up looking like."

dashgen recipe diff <name>
                Compares two recipe files (e.g. before/after edit) and
                shows panel-level differences in their effect on a fixture.
                Reads --fixture-dir.
```

### 11.3 Updated subcommand: `dashgen lint`

The existing `dashgen lint` subcommand continues to operate on rendered dashboard.json bundles. It is unchanged. A separate concept from `dashgen recipe lint`.

---

## 12. Migration Plan (all 44 recipes)

### 12.1 Tier mapping

Every existing Go recipe maps to exactly one YAML recipe under the schema in §4. The table below pins the mapping.

| Go recipe | YAML pattern | Notes |
|---|---|---|
| `service_http_rate` | Tier A: `type: counter` + `any_trait: [service_http]` | direct |
| `service_http_errors` | Tier B: `+ has_label_any: [status_code, code]` | uses `firstLabelOf` for legend |
| `service_http_latency` | Tier B: `#HistogramQuantileRecipe`, quantiles 0.5/0.95/0.99 | bucketName helper |
| `service_cpu` | Tier C-light: `name_equals_any: [process_cpu_seconds_total, container_cpu_usage_seconds_total]` | one panel, no pair |
| `service_memory` | same shape as service_cpu | direct |
| `service_grpc_rate` | Tier A: `type: counter` + `any_trait: [service_grpc]` | direct |
| `service_grpc_errors` | Tier B: `+ has_label: grpc_code`, legend filters `OK` | matcher predicate is straightforward; the `grpc_code != "OK"` filter is a query-template detail (`{ grpc_code!="OK" }` literal) |
| `service_grpc_latency` | Tier B: histogram + traits + bucketName | direct |
| `service_goroutines` | Tier A: `name_equals: go_goroutines` | direct |
| `service_gc_pause` | Tier C: type-dispatch via `requires_metric_type` | §5.2.6 |
| `service_db_query_latency` | Tier C: `none_trait` + `name_contains_any` | §5.2.5 |
| `service_tls_expiry` | Tier B: `name_has_suffix` (3 alternatives via `any_of`); query has `(m - time()) / 86400` | template handles the `time()` literal |
| `service_cache_hits` | Tier C: `pair_with: suffix_swap _cache_hits_total ↔ _cache_misses_total` | direct |
| `service_client_http` | Tier C: `name_contains: client` + `has_label_any: [status_code, code]` | direct |
| `service_db_pool` | **Split into two recipes**: `service_db_pool_go_sql_stats` and `service_db_pool_pgxpool` | per §8.3 |
| `service_job_success` | Tier C: `pair_with: suffix_swap _jobs_succeeded_total ↔ _jobs_failed_total`, `on_missing: warn` | direct |
| `service_kafka_consumer_lag` | Tier B: `name_equals_any: [kafka_consumergroup_lag, kafka_consumergroup_lag_sum]` | direct |
| `service_request_size` | Tier B: §5.2.3 | direct |
| `service_response_size` | Tier B: same as request_size | direct |
| `infra_cpu` | Tier A: `name_equals: node_cpu_seconds_total`, mode breakdown via `preferred_labels` | direct |
| `infra_memory` | Tier C: `pair_with: explicit node_memory_MemTotal_bytes ↔ node_memory_MemAvailable_bytes` | direct |
| `infra_disk` | Tier C: `pair_with: suffix_swap _avail_bytes ↔ _size_bytes` (mirror of filesystem_usage) | possible consolidation with filesystem_usage; keep separate to preserve current goldens |
| `infra_network` | Tier C: split into `infra_network_receive` and `infra_network_transmit` (each with its own panel), or single recipe with two panels | choose single-recipe-two-panel for parity with current Go |
| `infra_load` | Tier A: `name_equals_any: [node_load1, node_load5, node_load15]` | one recipe, multiple panels |
| `infra_filesystem_usage` | Tier C: §5.2.4 | direct |
| `infra_file_descriptors` | Tier C: `pair_with: explicit process_max_fds` | direct |
| `infra_nic_errors` | Tier B: `name_matches: ^node_network_.*_(errs|drop)_total$` | direct |
| `infra_conntrack` | Tier C: `pair_with: explicit node_nf_conntrack_entries_limit` (paired against `node_nf_conntrack_entries`) | direct |
| `infra_disk_iops` | Tier B: `name_equals_any: [node_disk_reads_completed_total, node_disk_writes_completed_total]` | one recipe, two panels |
| `infra_disk_io_latency` | Tier B: `name_equals: node_disk_io_time_seconds_total`, weighted variant via second panel | direct |
| `infra_ntp_offset` | Tier A: `name_equals: node_timex_offset_seconds` | direct |
| `infra_interrupts` | Tier A: `name_equals: node_interrupts_total` | direct |
| `k8s_pod_health` | Tier A: `name_equals: kube_pod_status_phase` | direct |
| `k8s_container_resources` | Tier C: split into `k8s_container_cpu` and `k8s_container_memory` | per §8.3 |
| `k8s_restarts` | Tier A: `name_equals: kube_pod_container_status_restarts_total` | direct |
| `k8s_deployment_availability` | Tier C: `pair_with: explicit kube_deployment_status_replicas_available` (paired against `_spec_replicas`) | direct |
| `k8s_node_conditions` | Tier C: §5.2.7 | direct |
| `k8s_pvc_usage` | Tier C: `pair_with: suffix_swap _capacity_bytes ↔ _available_bytes` | direct |
| `k8s_oom_kills` | Tier B: `name_equals: kube_pod_container_status_terminated_reason` + label filter via query template | matcher checks name only; query-template embeds `{ reason="OOMKilled" }` literal |
| `k8s_apiserver_latency` | Tier B: `name_equals: apiserver_request_duration_seconds`, group by verb/resource | direct |
| `k8s_etcd_commit` | Tier B: `name_equals: etcd_disk_backend_commit_duration_seconds` | direct |
| `k8s_hpa_scaling` | Tier C: `pair_with: suffix_swap _current_replicas ↔ _desired_replicas` | direct |
| `k8s_coredns` | Tier C: `pair_with: explicit coredns_dns_requests_total`, `on_missing: warn` | direct |
| `k8s_scheduler_latency` | Tier B: histogram + bucketName + group by `result` | direct |

**Result of migration:** 44 Go recipes → 47 YAML recipes (3 splits: `service_db_pool` → 2, `k8s_container_resources` → 2, `infra_filesystem_usage`/`infra_disk` ambiguity resolved by keeping both). All Go files under `internal/recipes/<recipe>.go` are deleted.

### 12.2 Phased rollout

| Phase | Scope | Workers | Acceptance |
|---|---|---|---|
| **0 — Design freeze** | Lock the schema, helper namespace, error mapping. Write `docs/RECIPES-DSL.md` (this file) and `docs/RECIPES-DSL-ADVERSARY.md`. | 1 architect (opus) + 1 critic (opus) | Schema unifies all 47 example recipes; review sign-off |
| **1 — Loader + 5 representative recipes** | Implement `loader.go`, `template.go`, `matcher.go`, `pair.go`. Migrate 5 recipes: `service_http_rate` (A), `service_http_latency` (B), `service_request_size` (B), `infra_filesystem_usage` (C-pair), `service_gc_pause` (C-typedispatch). Co-exist with remaining 39 Go recipes. | 1 executor (opus) + 1 test-engineer (sonnet) | Goldens unchanged; per-recipe tests parameterized |
| **2 — `--recipes-dir` + scaffolder + lint** | `dashgen recipes` subcommand surface. XDG default. Override semantics. | 1 executor (sonnet) + 1 writer (sonnet) | `dashgen recipe lint` passes/fails correctly on adversarial corpus |
| **3 — Tier-A migration** | All 10 Tier-A recipes to YAML. Delete corresponding Go files. | 1 executor (sonnet) | Goldens unchanged |
| **4 — Tier-B migration** | All 12 Tier-B recipes to YAML. Delete corresponding Go files. | 1 executor (sonnet) | Goldens unchanged |
| **5 — Tier-C migration** | All 22 Tier-C recipes to YAML, including the 3 splits. Delete remaining Go files. | 1 executor (opus) + 1 verifier (sonnet) | Goldens unchanged after deliberate regen for split recipes (panel-UID changes for split names) |
| **6 — Adversary corpus & hardening** | Implement the adversary tests from `RECIPES-DSL-ADVERSARY.md`. Add resource limits. | 1 executor (opus) + 1 security-reviewer (opus) | All adversarial inputs handled per spec; no test runs forever or crashes the loader |

Each phase ships a green CI build. Phases 3-5 are golden-byte-stable except for the 3 splits in phase 5 which require a one-time golden refresh + clear changelog entry.

---

## 13. Determinism Contract

Same inventory + same recipe set ⇒ byte-identical `dashboard.json`, `rationale.md`, `warnings.json`. Guarantees:

| Step | Deterministic by |
|---|---|
| YAML parse | yaml.v3 returns sorted maps when re-marshaled; loader marshals to JSON before CUE, which sorts keys |
| CUE unification | CUE is order-independent; output value is canonical |
| Recipe load order | sorted by full path (built-in then user, lexicographic) |
| Match evaluation | inventory iteration is sorted; matcher has no random / time access |
| Pair resolution | snapshot lookup is sorted; pair name is computed deterministically |
| Template rendering | `text/template` map iteration is sorted by key; helper FuncMap has no time/env/random access |
| Panel UID | unchanged: `ids.PanelUID(dashboardUID, section, metricName, kind)` SHA-256[:16] |
| Sort order in registries | by Name |

A new test, `TestRecipesDSL_Determinism`, runs the full pipeline against every fixture twice and asserts byte-equality.

---

## 14. Backwards Compatibility

### 14.1 During phased migration (phases 1-5)

The loader registers BOTH Go recipes (via existing `init()`) AND YAML recipes (via new `data/**/*.yaml` embed) into the same registry. The Recipe interface is unchanged. Synth code is unchanged. `internal/app/generate` is unchanged.

### 14.2 After migration (phase 5+)

All Go recipe files deleted. Only the loader + helpers + matcher + pair + yaml_recipe remain in `internal/recipes/`. The Recipe interface is unchanged but only YAMLRecipe implements it.

### 14.3 External Go-module recipe packs

If a future v0.4 introduces external Go-module recipe packs (the `contrib/` path BIG_ROCKS proposed), they would register Go-implementing-Recipe-interface recipes via `init()` exactly as today. The `Recipe` interface is the public contract.

### 14.4 Schema versioning

`apiVersion: dashgen.io/v1` is the v0.3 schema. Future versions (v2, etc.) load alongside v1; the loader dispatches by `apiVersion`. v1 recipes never break.

### 14.5 User config locations

`$XDG_CONFIG_HOME/dashgen/recipes/` is the default; falls back to `~/.config/dashgen/recipes/` when XDG unset; `--recipes-dir` overrides. Documented in README + `docs/RECIPES-USER-GUIDE.md` (written in Phase 2).

---

## 15. Testing Strategy

### 15.1 Loader tests

- **Schema unification.** Each example recipe in §5.2 must unify cleanly with the schema. New `TestSchema_UnifiesExamples` runs every example through CUE.
- **Error mapping corpus.** A directory `internal/recipes/testdata/invalid/*.yaml` with deliberately-broken recipes; each pinned to an expected user-visible error message. Verifies error mapping (§9.3).
- **Adversarial corpus** (see ADVERSARY spec §5).
- **Determinism test** (§13).

### 15.2 Per-recipe tests (replacement for the 44 `<name>_test.go` files)

A single parameterized test, `TestRecipesYAML_MatchAndBuild`, walks `internal/recipes/data/**/*.yaml` plus the existing fixture set. For each recipe, it:

1. Loads the YAML.
2. Reads a sibling `<recipe>.testdata.json` that lists positive metrics, look-alike negatives, and expected panel structure.
3. Asserts Match returns true on positives, false on negatives.
4. Asserts BuildPanels returns the expected panel shape.

This replaces the per-recipe Go test boilerplate with one parameterized loop + 47 small JSON fixtures.

### 15.3 Golden tests

Existing `TestGolden_<Profile><Class>` tests run unchanged. Output must be byte-identical after migration (except where deliberate splits cause panel UID changes; these are noted in CHANGELOG).

### 15.4 Discrimination tests

Existing `TestDiscrimination_<Profile><Class>Realistic` tests run unchanged. They guard against recipes regressing into look-alikes; this is independent of the authoring format.

### 15.5 Coverage

`go test -race -count=1 -timeout 120s ./...` passes with ≥604 tests (current baseline) + new loader/matcher/pair/template/error-mapping tests. Estimated final count: 700-750.

---

## 16. Open Questions

These must resolve before Phase 1 starts:

- [ ] **CUE library version.** Pin to a specific minor version of `cuelang.org/go`. Survey current upstream stability; pin to most recent compatible with Go 1.25.
- [ ] **Schema v1 freeze.** Once Phase 1 ships, the v1 schema is the public contract. Any breaking change requires a v2 sibling. Confirm willingness to commit.
- [ ] **Helper namespace authority.** Who owns adding new helpers? Proposed: any new helper requires a docs PR + ≥2 user-recipe demand cases. Confirm policy.
- [ ] **`--recipes-dir` security envelope.** Should the loader follow symlinks within the dir? Reject world-writable files? Validate file permissions? Default position: follow symlinks (within the dir's immediate subtree only); no permission check (unix philosophy: user decides).
- [ ] **Override telemetry.** When a user recipe overrides a built-in, should we warn once at startup, every run, or never? Proposed: warn once with `WARN level` log; idempotent.
- [ ] **Split-recipe golden refresh policy.** The 3 deliberate splits in §12.1 (db_pool, container_resources, possibly network) require a one-time golden refresh because panel UIDs change. Confirm this is acceptable.
- [ ] **Quantile fan-out.** A panel with `quantiles: [0.5, 0.95, 0.99]` emits 3 queries on the same panel. The current Go implementation does the same; confirm this is the desired user model vs. one-quantile-per-panel.
- [ ] **String fallback unit.** The CUE schema accepts arbitrary `unit: string` as a fallback. Should the loader emit a warning for non-canonical units to encourage standardization? Default: yes.

---

## 17. Acceptance Criteria

The DSL ships when ALL of the following hold:

1. All 44 (now 47) recipes have YAML representations validated by `schema.cue`.
2. All `internal/recipes/<recipe>.go` files are deleted.
3. `dashgen generate` against every fixture in `testdata/fixtures/` produces byte-identical `dashboard.json`/`rationale.md`/`warnings.json` to the v0.2.0 tag, except for the explicitly-noted splits in §12.1.
4. `--recipes-dir` works: a fresh user-authored YAML in a temp directory loads and fires.
5. `dashgen recipe lint` correctly accepts every example in §5.2 and rejects every invalid recipe in `testdata/invalid/`.
6. The adversary test corpus from `RECIPES-DSL-ADVERSARY.md` passes (every malicious input is rejected, contained, or rendered safe per spec).
7. `go test -race -count=1 -timeout 120s ./...` passes with ≥700 tests; zero races; zero panics.
8. The 5-stage validate pipeline still gates every emitted query (no recipe bypasses safety).
9. Determinism test passes (§13).
10. The `RECIPES.md`, `SPECS.md`, and `CODEBASE_MAP.md` docs are updated to reflect the new authoring contract.

---

## Appendix A: Why "go-template" instead of "CUE expressions"

CUE has native string interpolation (`"\(value)"`) and value composition. In principle, the query template could be a CUE expression rather than a Go `text/template` string. We chose Go templates because:

1. **Helper functions.** Recipes need `safeGroupLabels(metric, ...preferred)` which is non-trivial Go logic (filter banned labels, dedupe, sort). CUE has no user-defined functions; we'd embed pre-computed values into CUE every render, which is awkward.
2. **Loop constructs.** `text/template` has `{{ range }}` for iterating over a label set with separators. CUE's list comprehensions are powerful but less idiomatic for string assembly.
3. **Operator familiarity.** Go template syntax is widely known (Helm, Kubernetes manifests, Hugo). User-recipe authors who don't know CUE may know `{{ }}`.
4. **Caching.** `*template.Template` parses once at load time; renders are cheap. Same is true for CUE, but Go's stdlib needs no dependency.

The split "CUE for structure, text/template for query strings" is the operative design.

---

## Appendix B: Example Repository Layout (Hypothetical User Pack)

```
~/.config/dashgen/recipes/
  mycorp_queue_depth.yaml
  mycorp_lag_seconds.yaml
  mycorp_throughput_total.yaml
```

Each is a single YAML file authored by the platform team. They are loaded automatically on every `dashgen generate`. Adding a fourth recipe is `cp template.yaml mycorp_new_metric.yaml && vim mycorp_new_metric.yaml`. No fork. No build. No PR.

This is the strategic payoff.

---

## Document History

| Date | Author | Change |
|---|---|---|
| 2026-04-27 | initial draft | Full spec written; supersedes BIG_ROCKS "stay in Go" once Phase 0 review converges. |
