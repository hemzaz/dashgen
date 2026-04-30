# Big Rocks

> **Strategic-revisit document.** Captures forward-looking specs that aren't
> committed to action yet. Each rock has a forcing function for when to
> revisit; small (<1-day) follow-ups live in [`V0.4-QUEUE.md`](V0.4-QUEUE.md)
> instead.
>
> Two themes:
>
> - **§A — v0.4+ rocks (active queue).** Specs + adversary specs for logic
>   patterns surfaced by surveying `FUSAKLA/autograf` (Go) and
>   `uber/grafana-dash-gen` (TypeScript) on 2026-04-30. Each rock is sized,
>   threat-modeled, and gated by a forcing function. Nothing here is
>   committed; this is the design surface to revisit when triggers fire.
> - **§B — historical postscript: recipe-authoring debate (closed).** A short
>   summary of the v0.2→v0.3 strategic conversation. The action items from
>   the original analysis are now relics (Phase 4B shipped the scaffolder; v0.3
>   shipped the YAML DSL the original doc had argued against). Preserved only
>   for the principle that survived: *"don't grow a worse Starlark"* — the v0.3
>   schema's bounded predicate-budget + closed helper namespace honors it.
>
> Companion docs (current, not relics):
> - [`RECIPES-DSL.md`](RECIPES-DSL.md) — schema spec (current contract).
> - [`RECIPES-DSL-ADVERSARY.md`](RECIPES-DSL-ADVERSARY.md) — DSL threat model + 20 adversary fixtures (T7.1 implemented).
> - [`RECIPES-CLI.md`](RECIPES-CLI.md) — CLI surface + 10 CLI adversaries (T7.2 implemented).
> - [`V0.4-QUEUE.md`](V0.4-QUEUE.md) — small-item queue.

---

# §A. v0.4+ Big Rocks (active)

Each rock has:
- **Why** — the gap.
- **Spec** — concrete schema/API/behavior.
- **Adverse spec** — numbered threat enumeration with mitigations.
- **Forcing function** — what trigger justifies revisiting.
- **Effort** — rough sizing.
- **Out of scope** — what NOT to bundle.

## Index

- [BR1 — `dashgen publish` (direct Grafana upload)](#br1--dashgen-publish-direct-grafana-upload)
- [BR2 — Heatmap panel kind for histograms](#br2--heatmap-panel-kind-for-histograms)
- [BR3 — Pair-overlay rendering (`pair_with.render: overlay`)](#br3--pair-overlay-rendering-pair_withrender-overlay)
- [BR4 — Migrate `internal/render/grafana` to `grafana-foundation-sdk`](#br4--migrate-internalrendergrafana-to-grafana-foundation-sdk)
- [BR5 — Auto namespace-prefix row grouping](#br5--auto-namespace-prefix-row-grouping)
- [BR6 — Grafana template variables (`$instance`, `$job`, `$namespace`)](#br6--grafana-template-variables-instance-job-namespace)
- [BR7 — Alert emission alongside panels](#br7--alert-emission-alongside-panels)

Items deliberately NOT here (filed in `V0.4-QUEUE.md` because <1-day each):
`$__rate_interval` swap, annotations, time-metric auto-detect, `--selector '{app="foo"}'`.

---

## BR1 — `dashgen publish` (direct Grafana upload)

### Why

Today users run `dashgen generate --out ./dashboards` then manually `curl -X POST` or paste JSON into the Grafana UI. The e2e harness (commit `d6d5d39`) already proves the round-trip works against real Grafana. Productizing as `dashgen publish` removes the manual step. Both autograf (`pkg/grafana/manage.go`) and uber's `src/publish.ts` ship this; both are well-trodden patterns.

### Spec

New CLI subcommand:

```
dashgen publish <bundle-dir-or-files...>
  --grafana-url URL          required; HTTPS-only by default
  --token-env GRAFANA_TOKEN  required; reads from env, never accepts a flag value
  --folder NAME              optional; created if missing (idempotent)
  --datasource-uid UID       optional; rewrites datasource refs in dashboards before upload
  --dry-run                  parse + diff against current Grafana state, no writes
  --insecure                 allow http:// (warn loudly)
  --timeout DURATION         per-request, default 30s
```

Behavior:
- Reads dashboards from `<bundle-dir>/**/dashboard.json` OR explicit file list.
- Each dashboard's UID is deterministic (synth derives from recipe-name set; see `internal/synth`). Grafana upsert-by-UID is idempotent.
- Folder: `EnsureFolder(name)` — list, create if missing.
- Per dashboard: `POST /api/dashboards/db` with `overwrite: true`.
- Output (text or `--output json`): one line per dashboard with status `created|updated|unchanged|failed`.
- Exit codes: 0 all success, 1 partial, 2 input error, 5 transport/auth, 6 Grafana-rejected.

Implementation: borrow autograf's pattern of using `github.com/grafana/grafana-openapi-client-go`. Don't hand-roll; the official client handles version differences cleanly.

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR1.A1 | Token in shell history (user types `--token X`) | NEVER accept token as a flag value. Only `--token-env VAR_NAME` reads from env at exec. Reject `--token=...` with helpful error. |
| BR1.A2 | SSRF — `--grafana-url` points at internal/AWS-metadata service | Default to HTTPS-only. Reject `http://`, `file://`, link-local (`169.254.*`), loopback (`127.*`), RFC1918 (`10.*`, `192.168.*`, `172.16-31.*`) unless `--insecure` is passed. |
| BR1.A3 | Token leaked to stderr via response error body | Wrap Grafana errors through a redaction filter that strips bearer-token-like substrings before printing. |
| BR1.A4 | Concurrent runs racing on the same UID | Grafana upsert with `overwrite: true` is last-writer-wins. Acceptable; document. |
| BR1.A5 | Malicious / spoofed Grafana captures dashboard JSON (which may contain internal label names) | TLS verification ON by default; opt-in `--insecure-skip-verify` only. Optional `--ca-file` for self-signed. |
| BR1.A6 | Folder-name injection (`Production/../admin`) | Reject folder names containing `/`, `..`, NULL, control chars before sending to Grafana. |
| BR1.A7 | Bundle-dir symlink escape | Reuse `internal/recipes/loader.go` symlink-rejection pattern (T11) — refuse files whose realpath resolves outside the bundle dir. |
| BR1.A8 | Grafana rate-limits, dashgen retries forever | Default 3 retries with exponential backoff (250ms / 500ms / 1s); fail fast. `--retries N` to override. |
| BR1.A9 | Anonymous-auth Grafana exposes admin (test config bleeding into prod) | Warn loudly to stderr if response indicates anonymous-admin without auth header echo. |
| BR1.A10 | Token leak via panic stack trace | Wrap all token-bearing errors; never include the auth header in printed errors. |
| BR1.A11 | DNS rebinding | TLS verification mitigates for HTTPS. For `--insecure` http URLs, accepted residual risk. |
| BR1.A12 | Grafana version drift breaks the API client | Pin a specific `grafana-openapi-client-go` version in `go.mod`. CHANGELOG documents the supported Grafana version range. |

### Forcing function

Revisit when ANY:
- ≥3 GitHub issues request a publish/upload command.
- A user CI workflow explicitly cites manual upload as friction.
- The e2e harness drifts in a way whose fix would be the same fix in `dashgen publish`.

### Effort

**2–3 days.** The e2e harness already proves the protocol; the work is CLI scaffolding, config validation, and the adversary-mitigation surface.

### Out of scope

Two-way sync (Grafana → recipe). Folder hierarchy. Pre-flight schema validation against the live Grafana version. Grafana Cloud multi-org awareness.

---

## BR2 — Heatmap panel kind for histograms

### Why

Today the 7 histogram recipes (post T5.0.A multi-query) emit timeseries panels with 3 quantile lines (p50/p95/p99). The full bucket distribution is invisible. autograf (`pkg/grafana/panel.go: newHeatmapPanel`) renders histograms as Grafana heatmaps where the y-axis is the `le` bucket and color is rate. Operators see the WHOLE shape over time — critical for tail-latency investigation.

### Spec

Schema extension (`internal/recipes/schema.cue`):

```cue
#PanelTemplate: {
  ...
  kind: "timeseries" | "stat" | "gauge" | "heatmap"

  if kind == "heatmap" {
    quantiles?: _|_   // mutex: heatmap renders the full bucket distribution
    queries?: _|_     // single query (the bucket-rate)
  }
}
```

Renderer (`internal/render/grafana/`): emit Grafana heatmap with:
- `type: "heatmap"`
- `targets: [{ format: "heatmap", expr: "sum by (le) (rate(<bucket>[<window>]))" }]`
- `yAxis: { format: "<unit>" }` from panel.unit (default `s` for latency)

A recipe author opts in by setting `kind: heatmap` on a panel template that matches a histogram metric. CUE constraint enforces the metric-type compatibility.

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR2.A1 | `kind: heatmap` used with non-histogram metric | CUE constraint rejects at lint; loader rejects at load. |
| BR2.A2 | High-cardinality `le` blows up rendering | Grafana's `maxDataPoints` already throttles. Lint warns if a representative fixture shows >50 distinct `le` values. |
| BR2.A3 | Wrong unit on a heatmap (e.g. `s` on a byte histogram) | Schema requires `panel.unit`; canonical-unit lint already covers it (existing). |
| BR2.A4 | Format mismatch — Grafana heatmap requires `format: "heatmap"` on the target, not `time_series` | Renderer hardcodes `format: "heatmap"` for heatmap panels; covered by adversary fixture. |
| BR2.A5 | Schema mutex violation (`kind: heatmap` + `quantiles: [0.5]`) | CUE disjunction enforces exclusivity (mirrors T5.0.E `queries:`-vs-scalar mutex). Adversary fixture asserts rejection. |
| BR2.A6 | Recipe-author confusion (heatmap vs timeseries-with-quantiles) | RECIPES-DSL.md documents the choice; recipe scaffolder defaults to timeseries-with-quantiles unless `--kind=heatmap` passed. |
| BR2.A7 | Grafana version compat (heatmap panel JSON shape changed in Grafana 10) | Pin minimum supported Grafana version; document. |

### Forcing function

When ANY:
- A user explicitly requests heatmap visualization for a histogram recipe.
- BR4 (foundation-sdk migration) lands — at that point heatmap is "free" via the SDK's `heatmap` package, so this rock collapses into BR4's scope.

### Effort

**1–2 days standalone.** ~Half a day if BR4 lands first.

### Out of scope

Heatmaps for non-histogram metrics. Tooltip customization. Custom color schemes.

---

## BR3 — Pair-overlay rendering (`pair_with.render: overlay`)

### Why

Today `pair_with: explicit process_open_fds <-> process_max_fds` puts both halves into separate queries on the panel. autograf (`pkg/grafana/panel.go: addLimitTarget`) renders the partner as a dashed reference line on the SAME chart. Operators instantly see "current vs ceiling" without eye-scanning two lines. High-value for utilization patterns (open vs max fds, used vs total memory, allocated vs limit, current vs desired replicas).

### Spec

Schema extension (`internal/recipes/schema.cue`):

```cue
#PairWith: {
  ...
  render?: "separate" | "overlay"  // default: "separate" (backwards-compatible)
}
```

When `render: overlay`:
- Recipe emits ONE primary query (matched metric) + ONE overlay query (pair partner).
- Renderer adds a Grafana `fieldConfig.overrides` entry for the partner refId:
  - `custom.fillOpacity: 0`
  - `custom.lineStyle: { fill: "dash" }`
  - `color.mode: "thresholds"` (semantic, not hardcoded color)
- Implicit `requires_pair: true` (overlay panel without partner is incoherent).
- Restriction: only valid when matched metric uses single-query form (no `queries:` array, no quantiles). v1 limitation; can lift later.

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR3.A1 | Hardcoded red — bad for red/green colorblindness | Use Grafana semantic thresholds, not `fixed: red`. Operator's color config wins. |
| BR3.A2 | Missing pair on a metric that only has the primary half | Implicit `requires_pair: true` in overlay mode — recipe doesn't fire if partner absent. Existing `on_missing` policy covers. |
| BR3.A3 | Multi-query interaction (`queries: [...]` + overlay) | CUE constraint rejects the combination; document as v1 limitation. Adversary fixture asserts rejection. |
| BR3.A4 | Operator misreads the dashed line as "current" — could trigger wrong action | Renderer auto-prefixes overlay legend with `[limit]` (or recipe-author-controlled `legend_template` for overlay). Lint warns if overlay legend lacks a disambiguation marker. |
| BR3.A5 | Overlay used in alert (BR7) | Alert emission restricts to primary refIds; overlay refIds explicitly skipped. |

### Forcing function

When implementing or migrating a recipe makes the lack of overlay a noticeable operator-facing UX gap. Strong candidates: `infra_file_descriptors`, `infra_memory`, `service_db_pool` children, `k8s_hpa_scaling`, `k8s_deployment_availability`.

### Effort

**1–2 days.**

### Out of scope

Multi-overlay (3+ reference lines per panel). Threshold-based coloring of the primary line based on the overlay value. Overlay on heatmap (BR2 incompatible).

---

## BR4 — Migrate `internal/render/grafana` to `grafana-foundation-sdk`

### Why

We hand-roll Grafana JSON in `internal/render/grafana/`. autograf uses `github.com/grafana/grafana-foundation-sdk/go/{cog,common,dashboard,heatmap,prometheus,table,timeseries}` — Grafana's official Go SDK, type-safe, version-tracked, free panel kinds (heatmap, gauge, stat, table, …) for the cost of an `import`. We're losing time and accumulating bug surface every time we extend the renderer manually. **Biggest architectural rock in this doc.**

### Spec

Replace `internal/render/grafana/*.go` with SDK-driven builders.

Phased migration:
1. **Phase 1 — adapter layer.** Introduce `render.PanelBuilder` interface that abstracts over hand-rolled and SDK-built panels. New code uses the interface; existing code unchanged.
2. **Phase 2 — timeseries migration.** Convert the dominant panel kind first. Goldens regen, fixture-by-fixture review.
3. **Phase 3 — stat / gauge / multi-query migrations.** One panel kind per commit. Goldens regen per step.
4. **Phase 4 — delete the hand-rolled code.** When all panel kinds use SDK, drop the legacy renderer.
5. **Phase 5 — unlock new kinds.** Heatmap (BR2), table, log, gauge — almost-free additions.

Pin SDK version in `go.mod`. Document supported Grafana version range in `CHANGELOG.md`.

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR4.A1 | SDK breaking changes between Grafana versions | Pin SDK version; integration test against ≥2 Grafana versions in CI (extend e2e harness to matrix). |
| BR4.A2 | Supply chain — new Grafana-org dependency | `go.mod` pinned, `go.sum` verified, manual review of the SDK's transitive dep tree before merge. Document trust boundary. |
| BR4.A3 | Field-coverage gaps (SDK doesn't expose every field we use today) | Audit before migrating each panel kind. If gap exists, escalate: upstream a fix or keep that panel hand-rolled temporarily. |
| BR4.A4 | Golden regen burst overwhelms review | Phase 2–3 migrate one panel kind per commit. Per-commit golden diff is bounded. |
| BR4.A5 | SDK serializes JSON differently (field order, whitespace) | Goldens DO regenerate; this is expected. Per-commit diff sanity confirms only structural differences, not content. |
| BR4.A6 | SDK depends on Grafana's `cog` codegen — its own bug surface | Pin SDK; integration test catches regressions before they ship. |
| BR4.A7 | Performance regression — SDK might allocate more | T7.3 perf budgets must hold: re-run benchmarks per phase. Failure blocks the phase. |
| BR4.A8 | Backwards compat — older Grafana may reject SDK output | Document a version range; drop pre-Grafana-10 if needed. |
| BR4.A9 | SDK-coupling makes future renderer alternatives harder | The interface layer (Phase 1) keeps the SDK behind an abstraction. Replace implementation, keep interface, if needed. |
| BR4.A10 | Migration introduces silent semantic changes (e.g., default unit) | Per-panel-kind diff review against goldens; any non-trivial drift is a STOP. |

### Forcing function

When ANY:
- BR2 (heatmap) requires more than ~80 LOC of hand-rolled Grafana JSON.
- A new panel kind (gauge, table) is requested.
- A bug in the hand-rolled renderer ships that the SDK would have caught at compile time.
- The Grafana JSON spec changes in a way that requires a non-trivial renderer edit.

### Effort

**5–10 days.** v0.5-class. Highest long-term ROI in this doc; highest near-term cost.

### Out of scope

Replacing the IR (`internal/ir`). The IR stays Grafana-agnostic; the SDK lives behind the renderer boundary.

---

## BR5 — Auto namespace-prefix row grouping

### Why

The service-realistic dashboard has 24 panels in a flat list. autograf (`pkg/generator/tree.go`) builds a trie on `_`-split tokens and groups metrics into rows when ≥3 share a common prefix — so all `service_grpc_*` auto-row together, all `service_http_*` together. We group by SECTION (traffic / errors / latency / saturation); autograf groups by NAMESPACE PREFIX. Orthogonal and complementary.

### Spec

Renderer extension (`internal/render/grafana/`):

After existing section grouping, walk panels within each section and:
1. Build a prefix trie on metric name (split on `_`).
2. For each subtree where ≥3 panels share a common prefix, emit a Grafana row divider with the prefix as title.
3. Single-panel "groups" stay ungrouped within the section.

Ordering: rows alpha-sorted by prefix; panels within row sorted by current intra-section rules. Determinism preserved.

CLI flag: `--auto-rows` (default OFF for v0.4 to keep byte-stable goldens; flip default in v0.5 with documented golden regen).

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR5.A1 | Wrong prefix (`service_grpc` vs `service`) chosen as row | Tiebreak: longest common prefix shared by ≥3 panels wins. Documented. |
| BR5.A2 | Sensitive prefix as row title (`mycorp_customer_acme_*`) | Apply banned-label / redaction filter the recipe pipeline already uses to row titles. |
| BR5.A3 | Existing dashboards reorder under default | Off by default in v0.4; flip in v0.5 with explicit CHANGELOG entry. |
| BR5.A4 | Determinism break — trie ordering | Sort keys before walking; rows emit alpha order. Adversary test: same input → same row order across runs. |
| BR5.A5 | Empty section after grouping | Section headers without rows render unchanged. |
| BR5.A6 | Prefix collision with section name | Row title is the FULL prefix (`service_grpc`), not the leaf. Documented edge case. |

### Forcing function

When a profile dashboard exceeds 25 panels and operators report visual overload, OR when adding a 48th recipe pushes service-realistic past readability.

### Effort

**1–2 days.**

### Out of scope

Manual row authoring (operators specifying their own row layout). Custom row colors/icons. Cross-section grouping.

---

## BR6 — Grafana template variables (`$instance`, `$job`, `$namespace`)

### Why

Today's dashboards have ZERO interactivity. autograf's `--grafana-variables=instance,job` adds dropdown selectors so operators can scope to one instance / one job / one namespace. uber's library exposes the same via top-level `templating: [...]`. Single feature turns a static report into a working operator panel.

### Spec

Top-level recipe / synth-config addition:

```yaml
variables:
  - name: instance
    query: 'label_values(up, instance)'
    refresh: onTimeRangeChanged
    multi: false
    includeAll: true
  - name: job
    query: 'label_values(up, job)'
  - name: namespace
    query: 'label_values(kube_pod_info, namespace)'
    profiles: [k8s]
```

Recipe templates can reference variables in queries:

```yaml
query_template: 'sum by ({{ groupBy . }}) (rate({{ .Metric }}{instance=~"$instance",job=~"$job"}[{{ .Window }}]))'
```

`$instance` / `$job` syntax is Grafana-native — interpolated by Grafana at render time, NOT by dashgen at synth time.

`RenderContext.Variables` exposes declared variable names so templates can opt-in: `{{ if has .Variables "instance" }}{instance=~"$instance"}{{ end }}`.

Initial variable set per profile:
- service: `instance`, `job`
- infra: `instance`
- k8s: `namespace`, `pod`, `instance`

Schema (CUE): the `variables: [...]` list is closed; new variables go through helper-namespace governance (see [V0.4-QUEUE.md §3.4](V0.4-QUEUE.md): docs PR + ≥2 demand cases).

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR6.A1 | Cardinality blowup — `$instance="all"` on 10k instances | Per-variable cap warning at lint time if the live Prometheus has >1k values. |
| BR6.A2 | Label injection — operator picks a value that breaks PromQL | Grafana already escapes variable values in `=~"..."`; verify in adversary tests. |
| BR6.A3 | PromQL injection via variable substitution — Grafana variable is NOT parameterized | Restrict variable values to `[a-zA-Z0-9_-]+` regex; reject (at variable-definition time) variables whose `query` could return arbitrary strings. |
| BR6.A4 | Variable ↔ recipe coupling — recipe uses `$instance` but profile didn't declare it | CUE constraint: a recipe template referencing `$X` must have `X` in its profile's variables list. Lint catches at load. |
| BR6.A5 | Variable name collision with Grafana built-ins (`$__rate_interval`, `$__interval`) | Reserve the `$__*` namespace; reject user-defined variables there. |
| BR6.A6 | Empty variable on first dashboard load | `includeAll: true` + default value `.+` ensures non-empty render. |
| BR6.A7 | Refresh policy breaks browser perf | Default `refresh: onTimeRangeChanged` (lazy), not `onDashboardLoad` (eager). Document tradeoff. |
| BR6.A8 | Variable values leak PII (instance=user-laptop-name) | Banned-label filter applies to variable QUERIES the same way it does to recipe predicates. |
| BR6.A9 | Two recipes both want a `$service` variable with different definitions | Variables are dashboard-scoped, not recipe-scoped. First-declared wins; lint warns on conflict. |
| BR6.A10 | Grafana version drift — variable JSON shape changed in 10.x | Pin supported Grafana range; integration test. |

### Forcing function

When operators ask "how do I filter to one instance / namespace" 3+ times, OR when a power user requests interactive scoping. Likely concurrent with BR1 (publish) since users uploading to live Grafana are most likely to want variables.

### Effort

**2–3 days.**

### Out of scope

Custom variable types (interval, datasource, ad-hoc filter). Cross-dashboard variable sharing. Variables sourced from non-Prometheus (e.g., from a CMDB). Operator-authored variables outside the closed set.

---

## BR7 — Alert emission alongside panels

### Why

dashgen emits dashboards. SREs want alerts shipped alongside. uber's library has `Alert` + `Condition` objects (`src/alert/{alert.ts,condition.ts}`); the canonical Grafana unified alert shape is well-defined. Today users hand-author alerting rules separately, then drift over time as recipes evolve. dashgen with `alerts:` would emit a Grafana provisioning bundle for alert rules alongside the dashboard.

### Spec

Recipe schema extension (`internal/recipes/schema.cue`):

```cue
#Recipe: {
  ...
  alerts?: [...#AlertRule]
}

#AlertRule: {
  name:       string & strings.MinRunes(3) & strings.MaxRunes(160)
  on_panel:   string  // panel index reference (e.g., "0" or named)
  on_query:   string  // refId in the panel's queries array
  evaluator: {
    type:    "gt" | "lt" | "outside_range" | "within_range"
    threshold: number | [number, number]
  }
  for:        string  // duration like "5m"
  severity:   "critical" | "warning" | "info"
  labels?:    {[string]: string}
  annotations?: {summary: string, description?: string}
}
```

Output: dashgen emits `alerts.yaml` alongside `dashboard.json` per profile, in Grafana provisioning format:

```yaml
apiVersion: 1
groups:
  - name: <recipe-name>
    interval: 1m
    rules:
      - alert: <alert.name>
        expr: <recipe-query>
        for: <alert.for>
        labels: {severity: <alert.severity>, ...}
        annotations: {summary: ..., description: ...}
```

Notification policies, receivers, contact points: NOT in scope. Operator-specific.

### Adverse spec

| ID | Threat | Mitigation |
|---|---|---|
| BR7.A1 | Alert storm — bad threshold fires every evaluation | Schema requires `for: ≥1m`. Adversary fixture: `for: 0s` rejected. |
| BR7.A2 | Cardinality blowup — alert per-series across 10k instances | Alert query MUST aggregate. Lint warns if alert expr lacks `sum by`/`avg by`/`max by`. |
| BR7.A3 | Label leak — `labels: {tenant: $tenant}` echoes PII into alertmanager | Banned-label filter applies to alert labels (same set as recipe banned-labels). |
| BR7.A4 | PromQL injection via alert template | Alert query inherits recipe template-AST budget (T5 mitigation). Same caps. |
| BR7.A5 | Notification namespace collision (two recipes alert on same name) | Alert names scoped under `<recipe-name>.<alert-name>`; collision = lint error. |
| BR7.A6 | `noDataState` defaults that hide outages | Schema default: `noDataState: NoData` (not `OK`). Operator must explicitly opt-in to `OK`. |
| BR7.A7 | Alert references panel.refId that doesn't exist | CUE constraint validates `on_query` against panel's queries at lint time. |
| BR7.A8 | Multi-tenancy — alert rules span tenants in Grafana Cloud | `org_id` config supported but NOT defaulted. User explicitly scopes. |
| BR7.A9 | Severity inflation — every alert ships as `critical` | Lint warns if a profile has >50% `critical` alerts. |
| BR7.A10 | Recipe migration breaks alert wiring (panel index shifts) | Use `on_panel: <name>` (named ref), not numeric index. CUE rejects bare integers. |
| BR7.A11 | Alert rules diff from dashboard panels (drift) | `dashgen recipe diff` extends to compare alert rules. Same task. |
| BR7.A12 | Operator deletes alert in Grafana UI; next `dashgen publish` recreates | Document: dashgen-emitted alerts are source-of-truth; UI edits are ephemeral. `--no-overwrite-alerts` flag for opt-out. |

### Forcing function

When ANY:
- A vendor partner requests alerts as part of their recipe pack.
- ≥3 GitHub issues request alerts.
- An incident demonstrates the gap between dashgen dashboards and unmanaged alerts caused real on-call pain.

### Effort

**5–10 days.** v0.5-class. Strategically the highest-value rock; tactically the riskiest because alert semantics differ subtly across Grafana versions and tenancy models.

### Out of scope

Notification routing. Receivers / contact points. Silences. Alert manager configuration. Cross-cluster federation. SLO calculations (different feature; previously called out as a separate problem in §B persona analysis).

---

# §B. Historical Postscript: Recipe-Authoring Debate (closed)

**Status:** all action items shipped or dismissed. Preserved as a one-page summary, not a live recommendation.

### What was debated (2026-04-26)

Should recipe authoring move out of Go (44 hand-written `<recipe>.go` files) to a data format so users could author recipes for their own exporters without rebuilding the binary?

### What was rejected at the time

The original analysis recommended **staying in Go**: a recipe-scaffolder CLI plus a `contrib/` extension surface for vendor-authored Go modules. The argument: half the recipes (Tier C) had branching logic, multi-metric pairs, and predicate exclusions that no sane YAML schema could express without growing into Starlark.

### What actually shipped (v0.3.0, 2026-04-29)

The owner pushed back. v0.3 shipped the **YAML+CUE+text/template DSL** the original analysis had argued against — the "two-tier hybrid rejected for slippery-slope reasons." All 44 Go recipes are now 47 YAML files. The tier-C concerns turned out to be addressable with bounded primitives:
- `pair_with: explicit | suffix_swap` covered all multi-metric joins.
- `name_has_suffix` / `name_matches` / `name_contains_any` covered every name predicate.
- `requires_metric_type` + `queries: [...]` covered type-dispatch + multi-template panels.
- A closed helper namespace + 256-node template-AST budget kept the "worse Starlark" risk bounded.

The discipline that survived: the schema's bounded predicate budget + closed helper namespace + adversary corpus (T7.1) honor the original "don't grow a worse Starlark" principle even though the rest of the analysis was overtaken.

### Action items (now relics)

| Original Big Rock | Status |
|---|---|
| Big Rock 1 — Recipe Scaffolder CLI | **Shipped Phase 4B** as `dashgen recipe init` + `dashgen recipe scaffold`. |
| Big Rock 2 — `contrib/` Go extension surface | **Dismissed.** v0.3 went the YAML user-recipes-dir route; no Go contrib was ever built. |
| Big Rock 3 — AI-assisted recipe proposer | **Stale.** Referenced v0.2 Phase 5 (unknown-family AI grouping); not pursued in v0.3. Could resurface in v0.5+ if AI enrichment grows. |
| Big Rock 4 — Vendor design partnership | **Stale.** No vendor partner emerged; v0.3 shipped without one. |

### Personas analysis (still useful)

The four-persona breakdown (P1 platform engineer, P2 SaaS owner / SLO author, P3 vendor, P4 ML/AI ops) remains the best framing for *who* would author recipes. The conclusion that **P2's SLO authoring is a different problem** still holds — SLOs are not recipes; if SLO authoring becomes a v0.5+ theme, it's a separate design conversation.

### Forcing function (was)

The historical doc set forcing functions for revisiting the recipe-authoring question. All have effectively fired:
- v0.3 shipped the YAML format (the "format prototype" forcing function).
- The 44 Go recipes are gone (the "44 Go recipes are the contract" forcing function is moot).
- User extensibility shipped via `--recipes-dir` (the "users do not yet exist" forcing function is overtaken — they exist now but the surface is YAML, not Go contrib).

Nothing in the original §6/§7/§8/§9 lists is actionable today. If a real vendor pack proposal arrives, the relevant design surface is now the existing v0.3 DSL — not a hypothetical Go contrib system.

### What to read instead

For the current authoring contract, see [`RECIPES-DSL.md`](RECIPES-DSL.md). For the threat model that backs the bounded-predicate discipline, see [`RECIPES-DSL-ADVERSARY.md`](RECIPES-DSL-ADVERSARY.md). For the 8 user-facing CLI subcommands, see [`RECIPES-CLI.md`](RECIPES-CLI.md).
