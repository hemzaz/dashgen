# Big Rocks: Recipe Authoring & User Extensibility

> Strategic-revisit document. Not committed to action. Authored 2026-04-26
> by a parallel thinker team (architect / critic / analyst). Review at the
> end of v0.2 or when one of the explicit forcing functions in §9 fires.

## TL;DR

The owner asked: should recipe authoring move out of Go to a data format
(CUE / YAML / Starlark) so users can author recipes for their own
exporters without rebuilding the binary?

Three independent analyses converged on **no — not as the next
investment, and probably not at the YAML floor at all**. The reasons:
half of dashgen's existing recipes already exceed what a sane YAML
schema can express, the rebuild cycle is sub-second so it isn't the
ergonomic pain the framing implies, and the highest-leverage user
persona (vendor packs) is better served by an out-of-tree Go module
that registers itself in `init()`.

The recommended path for the foreseeable future is a two-step
sequence in v0.3+:

1. **Recipe scaffolder CLI** — `dashgen recipe new --metric
   foo_total --type counter --section traffic` emits a fully-tested
   Go file following the contract in `RECIPES.md`. This addresses the
   actual user pain ("I have my own metrics and the path to teach
   dashgen is unclear") without inventing a new authoring format.
2. **`contrib/` extension surface** — formalize external recipe packs
   as Go modules that call `recipes.Register(...)` from `init()`. Users
   build a custom binary with `dashgen build --with-contrib <pack>` (or
   `go install` directly). The Recipe interface is already three
   methods; nothing in the synthesis pipeline needs to change.

Everything below is the supporting evidence, the alternates considered,
and the open questions that gate any reinvestment.

## 1. Problem Re-Stated

The user framing was "rigid — I have to run build all over again every
time, which is very rigid." Re-stated more usefully:

> Operators with custom Prometheus exporters cannot teach dashgen about
> their own metric families. Today the only path is a contributor PR
> against the dashgen repo, which gates per-PR review + per-recipe
> tests + discrimination fixtures.

The build cycle itself is sub-second on a cold cache (`time go build
./cmd/dashgen` ≈ 2.5s warm). The pain is not compile time; the pain is
that authoring requires (a) Go fluency, (b) understanding the dashgen
testing patterns, and (c) a PR roundtrip with the dashgen team. Any
investment that doesn't move on (a)+(b)+(c) misses the actual ask.

## 2. Current State

The recipe interface (`internal/recipes/types.go:60-81`) is three
methods plus identity:

```go
type Recipe interface {
    Name() string
    Section() string
    Match(ClassifiedMetricView) bool
    BuildPanels(ClassifiedInventorySnapshot, Profile) []ir.Panel
}
```

44 recipes live in `internal/recipes/*.go`. They are typed strategy
objects, not strings — `BuildPanels` returns `[]ir.Panel`, never
Grafana JSON. Translation to Grafana schema happens in
`internal/render`. PromQL emitted by recipes still passes through the
five-stage validate pipeline (`internal/validate`) — recipes cannot
bypass safety.

**Tier distribution of the 44 recipes** (full breakdown in the appendix):

| Tier | Description | Count | % |
|------|-------------|-------|---|
| A | Pure data: type + 1 trait OR exact name + single template | 10 | 23% |
| B | Template-expressible only with non-trivial schema extensions (label predicates, suffix transforms, quantile-trio, status-label fallbacks) | 12 | 27% |
| C | Branching predicates with trait exclusion, multi-metric pairing, prefix-derived templating, dual code paths (summary vs histogram) | 22 | 50% |

**The 50% in Tier C is the load-bearing fact**: any data-driven
authoring format that does not ship Tier-C-equivalent expressivity
(which is to say, a real programming language) will only cover ~half
the catalog.

## 3. Design Space (Considered & Rejected for Now)

| Option | Verdict | One-line reason |
|--------|---------|-----------------|
| **CUE** (cuelang.org) typed schema | rejected | Real constraint validation but cannot express Tier-C branching; CUE-→-Go bindings still require rebuild. |
| **Starlark** (`go.starlark.net`) sandboxed Python-subset | rejected | Full conditionals, but every user recipe becomes executable code requiring review. Trust surface inverts. |
| **YAML / JSON + JSONSchema** | rejected | Lowest trust surface; cannot express label predicates, suffix transforms, pair-presence, or trait exclusion without growing into Starlark. |
| **WASM modules / Go plugins (`buildmode=plugin`)** | rejected | ABI fragility, sandbox escape risk, platform-specific binaries (plugins are Linux/macOS-only and version-coupled). Wrong tool for a static CLI. |
| **Two-tier hybrid: Go core + YAML user recipes** | rejected | Architect proposed; critic + analyst rejected on tier-distribution grounds. YAML floor must cover Tier B (label predicates, quantile-trio, suffix transforms, pair-presence) just to be credible — and at that point it's a worse Starlark. |
| **`contrib/` Go-module extension** | **adopted (v0.3+)** | External packs ship as Go modules with `init()` calling `recipes.Register(...)`. Full type system; existing test patterns transfer; no new schema language to maintain. |

The architectural rule that holds across every alternative: the
**Go-only boundary** — `internal/synth`, `internal/validate`,
`internal/safety`, `internal/classify`, `internal/render` — never
becomes reachable from user-authored content. Whatever the extension
surface, every emitted PromQL still flows through the five-stage
pipeline; trait derivation stays deterministic Go; Grafana schema
knowledge stays in render.

## 4. Why YAML Specifically Fails

Walking four representative existing recipes against any plausible
YAML schema:

- **`service_grpc_latency.go:71-75`** — appends `_bucket` only if the
  metric name doesn't already carry the suffix (Prometheus metadata
  returns the bare base name; the queryable series is `_bucket`). YAML
  needs a `name_transform: append_suffix_if_missing` primitive. The
  same logic recurs in `service_db_query_latency`,
  `k8s_apiserver_latency`, `k8s_etcd_commit`, `k8s_coredns`. Day-one
  schema bloat.

- **`service_db_query_latency.go:57-71`** — match histogram +
  `latency_histogram` trait + name contains "query"/"db"/"sql" + NOT
  `service_http` + NOT `service_grpc`. YAML needs trait exclusion
  lists, name substring sets with case-folding, and documented
  precedence — i.e., a boolean DSL embedded in YAML.

- **`service_request_size.go:28-41`** — match histogram + name
  `HasSuffix("_request_size_bytes")` (with or without `_bucket`) +
  must have `method` OR `handler` label. Adds `label_any_of` and
  `name_suffix_strip_then_match`; the next recipe needs `label_all_of`
  and `label_none_of`; the recipe after that needs
  `label_any_of_all_of`. This is how a config file becomes a
  programming language.

- **`infra_filesystem_usage.go:58-95`** — pair-metric: `Match` fires on
  `node_filesystem_size_bytes` but `BuildPanels` walks the snapshot to
  verify `node_filesystem_avail_bytes` is also present, then emits
  `(size - avail) / size`. The same pair-presence pattern occurs in
  `service_db_pool` (two parallel pairs) and `service_job_success`
  (suffix-stripping pairing). YAML needs multi-metric joins,
  prefix-derived templating, and conditional emission keyed on
  inventory contents — which is a query language, not a schema.

The rule-of-three test: by the time YAML expresses these four, you
have a worse Starlark. Five existing recipes already imply the
slippery slope; the slope is not hypothetical.

## 5. Personas & Demand

| Persona | Skill | Recipe set size | Distribution channel | Best surface |
|---------|-------|-----------------|----------------------|--------------|
| **P1 — Platform engineer** at 200-person company with `mycorp_*` exporter | Go-fluent (wrote the exporter) | 5–15 internal | private repo + `go install` | Go contrib |
| **P2 — SaaS owner** wanting per-product SLOs (`availability_ratio{product}`) | Bash + Helm; not Go | 1–3 | config repo | **Not recipe authoring** — declarative SLO config (separate feature) |
| **P3 — Database / exporter vendor** (ClickHouse, Vitess, Pinecone) | Go-fluent; ships Go binaries already | 10–25 per vendor | out-of-tree Go module | Go contrib + `init()` registration |
| **P4 — ML/AI ops** (PyTorch, vLLM, Ray) | Python-fluent, Go-curious | 8–15 per stack | forked `dashgen-ml` repo | Go contrib |

**P3 is the highest-leverage persona** — one vendor pack covers
thousands of operators. **P2 is the misconceived persona** — they want
declarative SLO configuration, not recipe authoring; treating these as
the same problem produces the worst design.

**Realistic 12-month demand**: 5–25 user-authored recipe packs total,
dominated by P3 (vendor) and P4 (ML stack). P1 contributes recipes
inside private repos that nobody outside their org sees. **None of
this justifies a new authoring format.**

## 6. Recommended Path

v0.3 work (only after the open questions in §8 get hard yes answers):

### Big Rock 1 — Recipe Scaffolder (~1 day)

A new CLI subcommand:

```bash
dashgen recipe new \
  --name mycorp_queue_depth \
  --metric mycorp_queue_depth \
  --type gauge \
  --section saturation \
  --profile service
```

Emits:

- `internal/recipes/mycorp_queue_depth.go` — fully formed recipe
  satisfying the `Recipe` interface, with sensible defaults pulled
  from the `--type` argument.
- `internal/recipes/mycorp_queue_depth_test.go` — table-driven Match
  test + BuildPanels assertions following the contract in
  `RECIPES.md` §1.
- A pointer to the discrimination fixture pattern — what to add to
  `testdata/fixtures/<profile>-realistic/` and how to regenerate
  goldens.

This solves Personas P1, P3, P4's actual ask without inventing
anything. It also doubles as documentation: a contributor learns the
recipe contract by reading the generated file.

### Big Rock 2 — `contrib/` Go Extension Surface (~2–3 days)

Formalize the out-of-tree pattern that the `Recipe` interface already
supports. Concrete deliverables:

- A documented contract for external packs: "import `dashgen/recipes`,
  call `recipes.Register(...)` from `init()`, ship a Go module."
- A reference example pack: `github.com/dashgen-contrib/example` with
  one or two recipes, full tests, fixture, README.
- A thin wrapper: `dashgen build --with-contrib
  github.com/<vendor>/<pack>@<version>` produces a custom binary.
  Under the hood this is `go install` with a pinned import; the build
  itself is sub-second per the critic's measurement.
- A version policy on the `Recipe` interface — once external packs
  exist, the interface becomes a public API and breaking changes go
  through a deprecation cycle.

Notably absent: no plugin architecture, no runtime loading, no schema
language. Users still get a static binary with deterministic output.

### Big Rock 3 — AI-Assisted Recipe Proposer (depends on Phase 5)

`dashgen inspect --propose-recipes` runs the existing inspect pipeline
plus the Phase 5 unknown-family AI grouping, surfacing metric clusters
that have no matching recipe. The output is a recipe scaffold draft
the user can pipe into Big Rock 1's scaffolder. This closes the
authoring loop:

```
dashgen inspect --prom-url ... --propose-recipes >| proposals.txt
# Review the proposals
dashgen recipe new --from-proposal proposals.txt
```

Coupling Phase 5 with the scaffolder gives users a one-command path
from "I have unmapped metrics" to "I have a fully-tested recipe in
my repo." This is concretely better than YAML, because the output is
real Go code with real tests — not a declaration the user has to
audit alone.

### Big Rock 4 — Vendor Design Partnership (prerequisite check)

Before any of the above, find ONE real prospective vendor (e.g.,
ClickHouse, Vitess, Pinecone) willing to author a contrib recipe pack
against the proposed surface. Validate the contract. If no vendor
bites, the entire surface is speculative; drop the rocks and revisit
when external demand is real.

## 7. What We Explicitly Do Not Do

- **No CUE / Starlark / YAML recipe authoring format** in v0.3 or
  later. The tier distribution and the slippery-slope evidence
  preclude it.
- **No Go plugins** (`buildmode=plugin`). ABI fragility + Linux/macOS
  only. Wrong tool.
- **No WASM modules**. Disproportionate complexity for the demand.
- **No "recipe gallery" / hosted index** until ≥3 real vendor packs
  exist out-of-tree.
- **No conflation of P2's SLO-config need with recipe authoring**.
  P2's pain is solved by a separate declarative SLO feature
  (operator-defined products → fixed panel template instances), which
  is its own design conversation.

## 8. Open Questions (Validate Before Any Investment)

- [ ] Is there ONE real prospective vendor or ML user willing to
  author a contrib recipe pack? **No vendor partner = no work.**
- [ ] What does Phase 5 (unknown-family AI grouping) actually deliver
  in practice? Does it cover enough of the "I have unmapped metrics"
  pain to make the scaffolder lower priority?
- [ ] Are external users actually asking for recipe authoring, or is
  this speculative? Wait until at least 3 GitHub issues request it.
- [ ] Does P2 (SLO config) deserve its own design conversation
  separate from recipe authoring? (Probably yes — different problem.)

## 9. Decision Forcing Functions

Revisit this document when ANY of the following fires:

- A real vendor approaches with a fully-formed pack proposal.
- Phase 5 ships and demonstrates either (a) the scaffolder is
  redundant, or (b) the AI gap is wider than expected.
- dashgen has ≥10 external users AND ≥3 of them request recipe
  authoring without rebuilding.
- A contributor lands a YAML/CUE/Starlark prototype as a PR. (The
  prototype itself is signal regardless of whether it's accepted.)

Until then: defer. The 44 Go recipes are the contract; the rebuild is
sub-second; users do not yet exist; the highest-leverage extension
surface (Go contrib with `init()` registration) requires zero new
machinery beyond a documented pattern. **Patience is the strategy.**

---

## Appendix: Tier Mapping of the 44 Recipes

For reference when revisiting. Counts are exact as of 2026-04-26.

**Tier A (10):** `service_cpu`, `service_memory`, `service_goroutines`,
`service_http_rate`, `infra_cpu`, `infra_load`, `infra_interrupts`,
`infra_ntp_offset`, `k8s_pod_health`, `k8s_oom_kills`, `k8s_restarts`.
Borderline: one of these is borderline-B; re-tier on revisit.

**Tier B (12):** `service_http_errors`, `service_grpc_rate`,
`service_grpc_errors`, `service_grpc_latency`, `service_http_latency`,
`service_request_size`, `service_response_size`, `service_tls_expiry`,
`infra_disk_iops`, `infra_disk_io_latency`, `infra_nic_errors`,
`service_kafka_lag`, `k8s_apiserver_latency`, `k8s_etcd_commit`,
`k8s_scheduler_latency`. The B/C boundary is fuzzy depending on which
schema features a hypothetical YAML floor would actually ship.

**Tier C (22):** `service_db_query_latency`, `service_db_pool`,
`service_job_success`, `service_cache_hits`, `service_client_http`,
`service_gc_pause`, `infra_filesystem_usage`, `infra_disk`,
`infra_memory`, `infra_conntrack`, `infra_file_descriptors`,
`infra_network`, `k8s_container_resources`,
`k8s_deployment_availability`, `k8s_hpa_scaling`, `k8s_pvc_usage`,
`k8s_node_conditions`, `k8s_coredns`. Plus pair-handling variants.

The conservative reading (B requires only label predicates + suffix
transforms + quantile-trio + status-label fallback) gives B = 27%; the
aggressive reading (B includes pair-presence and prefix derivation)
shrinks C and grows B into the 40-50% range. Neither flips the
strategic conclusion.
