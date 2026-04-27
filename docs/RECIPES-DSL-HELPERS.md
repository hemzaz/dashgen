# DashGen Recipe DSL — Helper Namespace + RenderContext

> Status: DRAFT (2026-04-27). Phase 0 deliverable T0.2. Locks the closed
> `text/template.FuncMap` and the `RenderContext` struct that
> `query_template`, `legend_template`, and `title_template` strings are
> rendered against. The schema in `internal/recipes/schema.cue` (T0.1)
> references helpers by name; this doc pins what those names mean.
>
> Companion specs:
> - [`RECIPES-DSL.md`](RECIPES-DSL.md) §7 — template engine spec.
> - [`RECIPES-DSL-ADVERSARY.md`](RECIPES-DSL-ADVERSARY.md) §2 (T15, I3–I5) — helper-namespace threat model.
> - [`V0.3-PLAN.md`](V0.3-PLAN.md) Phase 1A T1A.3 — helpers.go FuncMap binding.

---

## 1. Closed Namespace Policy

The helper namespace is **closed at compile time**. There is no user-supplied
`FuncMap` extension. Recipes invoke only the helpers below, plus the small set
of `text/template` stdlib functions called out in §4.6.

| Change kind | Process |
|---|---|
| **Add** a helper | Docs PR updating this file + `RECIPES-DSL.md` §7.3, with at least **2 user-recipe demand cases** documented in the PR description. Reviewed by the schema/helpers maintainer. |
| **Remove** a helper | Major version bump of the recipe `apiVersion` (e.g. `dashgen.io/v1` → `dashgen.io/v2`). Old recipes continue loading via the v1 namespace dispatch; new recipes use the v2 namespace. |
| **Change semantics** of a helper | Same as remove. Behavior changes are breaking even when the signature is preserved. |

The namespace is part of the v0.3 stability surface. The same maintainer who
owns `schema.cue` owns this file.

---

## 2. Banned Helpers (Permanent Forbid)

The following functions MUST NOT appear in any FuncMap version, regardless of
demand. Each violates a load-bearing invariant of the dashgen pipeline.

| Banned name | Rationale |
|---|---|
| `now`, `time`, `today` | **Determinism.** Clock access breaks the byte-equality contract (DSL §13). Same inventory + same recipes must always produce the same output. |
| `env`, `getenv` | **Hermeticity.** Environment lookup makes recipes machine-dependent. Goldens flake. Promotes leaking secrets via env into rendered queries (T18). |
| `exec`, `system`, `shellOut` | **Security.** Subprocess execution from a recipe is a remote-code-execution surface for any recipe pack the user installs. |
| `readFile`, `loadFile`, `slurp` | **Sandbox.** Filesystem read from inside a template breaks the loader's "recipes are pure value transforms" guarantee (DSL Non-Goal NG3). |
| `httpGet`, `fetch`, `request` | **Sandbox + determinism.** Network I/O from rendering is doubly disqualified. |
| `random`, `rand`, `uuid`, `now_unix_nano` | **Determinism.** Any source of nondeterminism poisons every downstream test. |
| `glob`, `walk`, `ls` | **Sandbox.** Filesystem inspection from a render. Recipes do not see the filesystem. |
| `eval`, `parse`, `compile` | **Recursion.** Templates may not load further templates (DSL §7.5 forbids `{{ define }}` / `{{ template }}` / `{{ block }}` for the same reason; helper-level evals are an even larger hole). |
| `regexpMatch`, `regexpReplace` (on user input) | **ReDoS / determinism.** Regex on label values would re-introduce the value-leak that I2 forbids. Regex on metric names is already done at match time, not render time. |

A helper that takes a *function* as an argument is also forbidden: passing a
template-defined function to a stdlib helper would smuggle code in via a back
door. Helpers in this namespace are **first-order only**.

---

## 3. RenderContext

Every render of a `query_template` / `legend_template` / `title_template`
string runs against an instance of this Go struct. The dot context (`.`) IS
this struct. Helpers receive it as their first positional argument when used
in `(ctx)` form.

```go
// RenderContext is the dot-context for every text/template render.
// Constructed once per (matched-metric, panel-template) pair at synth time.
type RenderContext struct {
    // Metric is the matched metric's name (already validated as ASCII-only
    // by classify; never user-controlled at render time).
    Metric string

    // Type is the metric's classifier-emitted type ("counter", "gauge",
    // "histogram", "summary"). Used by `requires_metric_type` panel filter
    // and by helpers that need to dispatch on type.
    Type string

    // Labels carries label NAMES only — never values. The map shape is
    // preserved for compatibility with text/template's `index` builtin
    // (used as `{{ index .Labels "mountpoint" }}` in fixtures); values
    // are deterministic placeholders or empty strings, never real label
    // values.
    //
    // ADVERSARY: I2 (label-value leak invariant). Every helper that
    // touches Labels MUST consume only the keys, never the values.
    Labels map[string]string

    // LabelList is the sorted slice of label names — preferred over Labels
    // for any helper that iterates, because text/template's `range` over
    // a map is sorted by key but using a slice removes any ambiguity.
    LabelList []string

    // ScopeFilter is the pre-rendered Prometheus matcher fragment for the
    // current synth scope (e.g. `job="$job", instance="$instance"`).
    // Templates embed it verbatim; they never construct it.
    ScopeFilter string

    // Window is the rate window for this panel (default "5m"; overridable
    // via panel.rate_window). Pre-validated against #RateWindow regex.
    Window string

    // GroupBy is the resolved safeGroupLabels result for this panel —
    // computed before render so templates can use the helper `groupBy .`
    // without re-running the safety logic.
    GroupBy []string

    // PreferredLabels is the panel.preferred_labels list (post-defaults).
    PreferredLabels []string

    // Quantile is the current quantile in the histogram-quantile iteration
    // (e.g. "0.99"). Empty string when the panel does not declare quantiles.
    Quantile string

    // Quantile100 is Quantile × 100, integer-formatted ("99" for 0.99).
    // Used in title_template strings: "HTTP latency p{{ .Quantile100 }}".
    Quantile100 string

    // Pair is the resolved pair-context iff pair_with was declared and
    // resolution succeeded. nil otherwise (panels with requires_pair: true
    // are skipped before render reaches them in that case).
    Pair *PairContext
}

// PairContext is the dot-context for the pair half of a Tier-C recipe.
// Like RenderContext.Labels, PairContext.Labels is names-only.
type PairContext struct {
    Name   string            // resolved pair metric name
    Type   string            // pair metric's classifier type
    Labels map[string]string // names-only (I2)
}
```

### 3.1 Invariants on RenderContext

| ID | Invariant | Enforcement |
|---|---|---|
| RC1 | `Labels` and `Pair.Labels` carry NO real label values. | Synth populates from `Descriptor.Labels` (names) only. Loader test asserts a tagged metric with sensitive label-value content cannot leak through render. |
| RC2 | `LabelList` is sorted lexicographically. | Synth sorts before assignment. |
| RC3 | `Metric`, `Pair.Name`, and items in `Labels` are 7-bit ASCII. | Already enforced at classify time + by `#MetricNameASCII` in schema. |
| RC4 | `Quantile`, `Quantile100`, `Window` are non-empty when their respective panels exercise them. | Render driver sets these; templates that reference them on panels without quantiles fail loudly via `Option("missingkey=error")`. |
| RC5 | `Pair` is nil OR fully populated. | Pair resolver returns either a complete `PairContext` or `nil` + error. |

---

## 4. Helper Catalog

Every helper below is bound at FuncMap-construction time in
`internal/recipes/template.go` (Phase 1A T1A.3). The listing here is the
contract; the Go implementation must match exactly.

### 4.1 `groupBy`

```go
// groupBy renders the comma-separated safeGroupLabels result for the
// current render context — the safe set of grouping labels chosen at
// synth time, already filtered against the banned-label list.
//
// Used in: sum by ({{ groupBy . }}) (...)
groupBy(ctx *RenderContext) string
```

**Semantics.** Returns `strings.Join(ctx.GroupBy, ", ")`. No re-computation
of grouping logic at render time — the heavy lifting (`safeGroupLabels`)
runs in synth.

**Determinism.** `ctx.GroupBy` is a sorted slice; same input → same output.

**Used by fixture(s):** `service_http_rate` (`sum by ({{ groupBy . }}) (...)`).

### 4.2 `groupByWith`

```go
// groupByWith returns the same comma-separated safe grouping as groupBy,
// but ensures each `extra` label is appended (deduplicated, in argument
// order). Used to add `le` for histogram_quantile or `quantile` for
// summaries.
//
// Used in: sum by ({{ groupByWith . "le" }}) (...)
groupByWith(ctx *RenderContext, extras ...string) string
```

**Semantics.** Concatenates `ctx.GroupBy` with each `extra` not already
present, in the order given. Returns a comma-joined string.

**Banned-label guard.** Any `extra` whose name appears in the banned-label
list is silently dropped (defense in depth — the schema's metric-name ASCII
constraint doesn't cover dynamic extras passed at template author time).

**Determinism.** Pure string operation on slices.

**Used by fixture(s):** `service_http_latency`, `service_request_size`,
`service_db_query_latency`, `service_gc_pause`.

### 4.3 `legendFor`

```go
// legendFor renders the Grafana legend template for this panel — the
// "{{job}} {{instance}} ..." pattern over the resolved grouping. Returns
// the empty string when GroupBy is empty.
//
// Used in: legend_template: "{{ legendFor . }}"
legendFor(ctx *RenderContext) string
```

**Semantics.** For each label name `l` in `ctx.GroupBy`, emits `{{<l>}}`;
joins with single spaces. (Note: the doubled curly braces are Grafana-side,
not Go-template-side. This output is itself a template that Grafana renders
client-side against the Prometheus result.)

**Determinism.** Stable iteration over the sorted `ctx.GroupBy` slice.

**Used by fixture(s):** All 7. (`legendFor` is the universal legend pattern.)

### 4.4 `bucketName`

```go
// bucketName ensures a metric name has the `_bucket` suffix expected by
// Prometheus's histogram_quantile function. Idempotent: passing an already-
// suffixed name returns it unchanged.
bucketName(s string) string
```

**Semantics.**

```
bucketName("http_request_duration_seconds")        → "http_request_duration_seconds_bucket"
bucketName("http_request_duration_seconds_bucket") → "http_request_duration_seconds_bucket"
```

**Determinism.** Pure suffix check + concat.

**Used by fixture(s):** `service_http_latency`, `service_request_size`,
`service_db_query_latency`, `service_gc_pause`.

### 4.5 `stripSuffix`

```go
// stripSuffix returns s minus suffix when it ends with suffix; otherwise s
// unchanged. Idempotent.
stripSuffix(s, suffix string) string
```

**Semantics.**

```
stripSuffix("http_request_size_bytes_bucket", "_bucket") → "http_request_size_bytes"
stripSuffix("http_request_size_bytes",        "_bucket") → "http_request_size_bytes"
```

**Determinism.** `strings.TrimSuffix`.

**Used by fixture(s):** `service_request_size`
(`bucketName (stripSuffix .Metric "_bucket")` to normalize a histogram
metric whose match included both `_request_size_bytes` and
`_request_size_bytes_bucket` aliases).

### 4.6 `text/template` stdlib functions in scope

`text/template` ships a small set of builtins — `index`, `len`, `print`,
`printf`, `println`, `eq`, `ne`, `lt`, `le`, `gt`, `ge`, `and`, `or`, `not`.
These remain in scope. The loader does not strip them.

**`index`** is the only stdlib builtin observed in the v0.3 example fixtures
(`{{ index .Labels "mountpoint" }}` in `infra_filesystem_usage`). It accesses
`Labels` by key — and per invariant RC1, `Labels` carries names only, so
`index` returns either an empty string or a deterministic placeholder. Real
values cannot leak through `index`.

**Forbidden** (loader rejects at parse, AST-walk):
`{{ define }}`, `{{ template }}`, `{{ block }}` — see DSL §7.5 + ADVERSARY T5.

---

## 5. Coverage Check

Mapping: helper → which of the 7 example recipes invokes it.

| Helper | service_http_rate | service_http_latency | service_request_size | infra_filesystem_usage | service_db_query_latency | service_gc_pause | k8s_node_conditions | Coverage |
|---|---|---|---|---|---|---|---|---|
| `groupBy`     | ✓ |   |   |   |   |   |   | 1 |
| `groupByWith` |   | ✓ | ✓ |   | ✓ | ✓ |   | 4 |
| `legendFor`   | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | 7 |
| `bucketName`  |   | ✓ | ✓ |   | ✓ | ✓ |   | 4 |
| `stripSuffix` |   |   | ✓ |   |   |   |   | 1 |

**Coverage status:** 5 helpers shipped. Every helper has at least one
fixture user. ✓

### 5.1 Helpers proposed but DEFERRED to first real user

The DSL §7.3 catalog listed five additional helpers that have no v0.3
fixture user. Per the §1 closed-namespace policy ("≥2 user-recipe demand
cases"), they are NOT included in the v0.3 FuncMap. Each gets added when a
demanding recipe lands.

| Helper | DSL §7.3 description | Demanding recipe (per §12.1 migration_tiers) | Status |
|---|---|---|---|
| `firstLabelOf(ctx, labels...)` | First label name from the list present in ctx.Labels. | `service_http_errors` (DSL §12.1: "uses `firstLabelOf` for legend") | **Add in Phase 4 / Tier-B migration** when `service_http_errors` lands. |
| `appendSuffix(s, suffix)` | Append suffix if not already present. Idempotent inverse of stripSuffix. | None in §5.2; speculative. | **Defer.** Add only when ≥2 recipes need it. |
| `defaultRateWindow()` | Returns the project's default rate window ("5m"). | None. `RenderContext.Window` already carries the per-panel value. | **Defer.** The context field supersedes the helper. |
| `joinLabels(labels)` | Comma-join a label list. | None — `groupBy` and `groupByWith` cover the cases. | **Defer.** |
| `quantile()` | Current quantile. Equivalent to `.Quantile`. | None — `.Quantile` is the field of record. | **Defer.** Helper would be a synonym for the context field; superfluous. |

**Recommendation to Phase 1A T1A.3 implementer:** ship the 5 fixture-used
helpers ONLY (`groupBy`, `groupByWith`, `legendFor`, `bucketName`,
`stripSuffix`). Add the deferred set incrementally as Tier-A/B/C migrations
demand them. Each addition gets a paired commit updating this doc + the
schema's documented helper list.

---

## 6. Threat-ID Gating

No helper currently in the v0.3 namespace gates a specific ADVERSARY threat.
The relevant invariants and their enforcement points:

| ADVERSARY ref | Concern | Where enforced |
|---|---|---|
| **T8** banned-label-grouping | `groupByWith` extras list must drop banned labels. | `groupByWith` impl (helpers.go banned-label guard); validate stage 4 catches anything that slips. |
| **T15** helper namespace abuse | No `now`/`env`/`exec`. | §1 policy + §2 ban list + Phase 1A loader's helper-coverage check (every name invoked in a parsed template must be in the FuncMap). |
| **I2** label-value leak | Helpers may not return label *values*. | `RenderContext` shape (Labels carries names only) + helper signatures take `*RenderContext` and return `string` derived from names + non-sensitive fields. |
| **I3** no `{{ define }}`/`{{ template }}`/`{{ block }}` | Loader-side AST walk. | Phase 1A T1A.3, not helper-level. |
| **I4** no missing-key fallback to `<no value>` | `template.Option("missingkey=error")` set at FuncMap construction. | Phase 1A T1A.3. |
| **I5** no calls to undefined helpers | `text/template` raises "undefined function" at parse. | Phase 1A T1A.3 (loader fails fast at recipe parse). |

### 6.1 Future-helper review checklist

When adding any helper:

1. [ ] **Pure.** No I/O. No goroutines. No global state.
2. [ ] **Deterministic.** Same input → same output forever, across machines.
3. [ ] **Bounded.** No loops over user input, or loops bounded by input size with documented bound.
4. [ ] **No reflection.** Concrete types only.
5. [ ] **Label-value-safe.** If the helper touches `Labels`, it consumes keys only — never returns or interpolates values.
6. [ ] **No string-eval.** Never compile-and-run a string as a template fragment.
7. [ ] **Doc + ≥2 demand cases** in the PR description (§1 policy).
8. [ ] **Listed here** in §4 with semantics + fixture user + threat-ID gating.

A helper that fails any of 1–6 is rejected at review.

---

## 7. Mapping to the Existing `internal/recipes/helpers.go`

The current Go-recipe codebase has two helper implementations:

| Existing function | New v0.3 binding | Notes |
|---|---|---|
| `safeGroupLabels(m, preferred...)` | Called by synth before render to populate `RenderContext.GroupBy`. NOT exposed as a template helper. | Synth-time precomputation; recipes cannot bypass it. The banned-label list lives in `helpers.go` and remains the single source of truth. |
| `legendFor(labels []string)` | Same logic, but template-bound as `legendFor(ctx)` — extracts `ctx.GroupBy` then defers to the existing function. | Backwards compatible. |

The remaining helpers (`groupBy`, `groupByWith`, `bucketName`, `stripSuffix`)
are NEW Go functions added in Phase 1A T1A.3. They are pure string operations
and have no equivalents in the Go-recipe world (Go recipes inlined the logic).

---

## 8. v0.3 Decision Record

The following decisions are pinned by this document. Reverse only via the
process in §1.

| # | Decision | Rationale |
|---|---|---|
| H1 | The v0.3 FuncMap ships exactly 5 helpers: `groupBy`, `groupByWith`, `legendFor`, `bucketName`, `stripSuffix`. | Minimum needed to render every example in DSL §5.2. |
| H2 | DSL §7.3's `firstLabelOf`, `appendSuffix`, `defaultRateWindow`, `joinLabels`, `quantile` are DEFERRED. | No fixture user; closed-namespace policy requires demand. |
| H3 | The text/template stdlib (`index`, `len`, `eq`, etc.) remains in scope. `{{ define }}` / `{{ template }}` / `{{ block }}` are forbidden by the AST walk. | Stdlib pure-string ops are determinism-safe. The three forbidden directives enable cross-template references which would break the closed-FuncMap guarantee. |
| H4 | `RenderContext.Labels` carries names only. | I2 invariant; the only safe way to expose a `map[string]string` to user templates. |
| H5 | `RenderContext.Window` supersedes a `defaultRateWindow()` helper. | One mechanism per concept; the helper would be redundant. |
| H6 | `RenderContext.Quantile` / `Quantile100` supersede a `quantile()` helper. | Same. |

---

## Document History

| Date | Author | Change |
|---|---|---|
| 2026-04-27 | initial draft | Closed namespace policy + 9-entry banned list + RenderContext spec + 5-helper catalog with fixture coverage + 5-helper deferral list + threat-gating notes. Companion to RECIPES-DSL.md §7. |
