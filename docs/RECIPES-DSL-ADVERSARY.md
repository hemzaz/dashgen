# DashGen Recipe DSL — Adversary Specification

> Status: DRAFT (2026-04-27). Companion to [`RECIPES-DSL.md`](RECIPES-DSL.md).
> Defines the threat model for the YAML wire format + CUE schema + text/template
> runtime described there. Pairs with the project-wide trust doc
> [`ADVERSARY.md`](ADVERSARY.md), which covers higher-level trust boundaries.

---

## 0. Scope

This document covers attacks that originate from **user-authored recipe content** — YAML files loaded via `--recipes-dir` or `$XDG_CONFIG_HOME/dashgen/recipes/`. It does NOT cover:

- Attacks against the Prometheus backend (out of scope; Prometheus is upstream of dashgen).
- Attacks against the AI enrichment seam (covered by `V0.2-PLAN.md` §2.5 and the existing redaction guard).
- Supply-chain attacks against `cuelang.org/go` or `gopkg.in/yaml.v3` (handled by Go module checksums + dependency review).
- Attacks against the CLI process from outside the recipe loader (covered by `RECIPES-CLI.md` §9, which addresses CLI-specific threats).

The threat boundary is: **the loader treats every recipe as untrusted.** Built-in and user-authored recipes go through the same validation pipeline — there is no privileged path for built-ins.

---

## 1. Threat Model

### 1.1 Trust boundaries

```
┌────────────────────────────────────────────────────────┐
│   Trusted: dashgen binary, schema.cue, helper FuncMap   │
│   ┌──────────────────────────────────────────────┐     │
│   │   Untrusted: every YAML recipe                │     │
│   │   - built-in recipes (data/**/*.yaml)         │     │
│   │   - user recipes (--recipes-dir, XDG)         │     │
│   └──────────────────────────────────────────────┘     │
│                                                          │
│   Trusted post-load:                                     │
│   - Compiled match predicates (regex, traits, names)     │
│   - Compiled templates (parsed, FuncMap-bound)           │
│   - Resolved registry (sorted, dedup'd)                  │
└────────────────────────────────────────────────────────┘
```

The loader's job is to convert untrusted YAML into a trusted registry. After load, the registry is treated as safe by synth/validate/render. **The validation pipeline runs at load time once, not per render.** Anything that survives load is trusted.

### 1.2 Attack surfaces

| Surface | Description |
|---|---|
| **AS1** YAML parser | yaml.v3 parses untrusted bytes. |
| **AS2** CUE schema unification | cue.Value.Unify parses + evaluates user-supplied fields. |
| **AS3** Match predicate evaluation | matcher walks user-supplied predicate AST against ClassifiedMetricView. |
| **AS4** Regex compilation | name_matches strings compile to *regexp.Regexp. |
| **AS5** Template parsing | text/template parses query/legend/title strings. |
| **AS6** Template rendering | text/template renders against RenderContext per-match. |
| **AS7** Pair resolution | suffix_swap/prefix_swap/explicit produce candidate metric names. |
| **AS8** Filesystem traversal | --recipes-dir walks user-controlled paths. |
| **AS9** PromQL emission | rendered queries flow into validate (then to Prometheus). |

### 1.3 Out-of-scope threats

- The user passes `--recipes-dir /etc` and exposes their own files. Dashgen is a CLI run by the user; voluntary self-harm is out of scope.
- The user installs a malicious dashgen binary. Out of scope (pre-binary trust).
- Network attacks against Prometheus. Upstream concern.
- Side-channel attacks (timing, cache). The deterministic pipeline doesn't expose secrets; no side-channel concern.

---

## 2. Threat Catalog

Each threat below has: **ID**, **description**, **attack surface**, **blast radius**, **mitigation**, **residual risk**, **adversary test name**.

### T1 — YAML billion-laughs / anchor expansion DoS

- **AS1**, AS2.
- **Description:** YAML supports anchors and aliases. A document with deeply nested anchor expansions (the "billion laughs" attack) can blow up parse memory.
- **Blast radius:** Loader OOM, dashgen process crash. No data exfiltration.
- **Mitigation:**
  1. yaml.v3 in modern versions caps anchor expansion depth to 1024 — this is enforced.
  2. **Per-file size cap** (default 64 KB) at the discovery layer, before parse begins.
  3. **Total recipe byte budget** (default 4 MB across all recipes) checked at discovery; once exceeded, additional files are rejected with a clear error and load continues with only the budgeted set.
- **Residual risk:** Low. A 64 KB file cannot expand to billions of nodes within yaml.v3's depth cap.
- **Adversary test:** `testdata/adversarial/yaml_billion_laughs.yaml`.

### T2 — Recipe count exhaustion

- **AS8**, AS1.
- **Description:** User points `--recipes-dir` at a directory with millions of `.yaml` files. Loader walk + parse loop exhausts memory or time.
- **Blast radius:** Process slow startup or OOM.
- **Mitigation:**
  1. **Per-directory file count cap** (default 1024). Exceeded → error during discovery; no partial load.
  2. **File size cap** (T1's mitigation 2) per file.
  3. **Total budget** caps cumulative bytes regardless of count.
- **Residual risk:** Low.
- **Adversary test:** `loader_test.go::TestDiscover_FileCountCap`.

### T3 — Catastrophic CUE evaluation

- **AS2**.
- **Description:** A pathological YAML structure that, when unified against `#Recipe`, causes CUE evaluator to take exponential time. CUE is sound + decidable, but evaluator implementations have practical complexity bounds.
- **Blast radius:** Loader hang.
- **Mitigation:**
  1. **Per-file evaluation deadline** (default 5 seconds, wall clock). Loader uses `context.WithTimeout` on the cue.Context.
  2. CUE's stdlib eval is bounded by structural depth; combined with per-file size cap, complexity is bounded.
- **Residual risk:** Medium. CUE pre-1.0 has had performance regressions. Pin a known-good version; track upstream.
- **Adversary test:** `testdata/adversarial/cue_eval_pathological.yaml` + a unit test that asserts the deadline triggers.

### T4 — Regex denial-of-service (ReDoS)

- **AS4**, AS3.
- **Description:** User supplies `name_matches: "(a+)+$"` — a polynomial-blowup regex when matched against a long-enough metric name.
- **Blast radius:** Per-match CPU spike during synth (not load).
- **Mitigation:**
  1. **Go's `regexp` is RE2-based — NO backtracking.** Worst case is linear time in input length. ReDoS is structurally impossible. This is the primary mitigation.
  2. Per-pattern length cap (default 256 runes) prevents pathologically long patterns.
  3. Anchoring requirement: regex must start with `^` AND end with `$`. (Soft requirement — loader emits a WARN if missing, not an error, to avoid breaking common patterns.)
  4. Per-match deadline at synth time (per-recipe, default 100ms wall clock) — defense in depth even though RE2 makes it unnecessary for regex itself.
- **Residual risk:** Very low (RE2 makes ReDoS structurally impossible).
- **Adversary test:** `testdata/adversarial/redos_pattern.yaml` + a benchmark asserting linear time.

### T5 — Template parse bomb

- **AS5**.
- **Description:** Template with deeply nested `{{ if }}` / `{{ range }}` / `{{ with }}` directives, or a template that defines many sub-templates via `{{ define }}`.
- **Blast radius:** Template parse time / memory.
- **Mitigation:**
  1. **`{{ define }}`, `{{ template }}`, `{{ block }}` directives are disallowed.** The loader walks the parsed AST (`text/template/parse`) and rejects any TemplateNode or BlockNode. (See `RECIPES-DSL.md` §7.5.)
  2. **Per-template size cap** (default 2048 runes per `query_template`, 160 per `legend_template`/`title_template`).
  3. **AST node budget** (default 256 nodes per parsed template). Exceeded → load error.
- **Residual risk:** Low.
- **Adversary test:** `testdata/adversarial/template_define_directive.yaml`, `testdata/adversarial/template_nested_if.yaml`.

### T6 — Template render-time DoS

- **AS6**.
- **Description:** Template rendered against a metric whose label set is enormous, causing a `{{ range .Labels }}` loop to balloon output.
- **Blast radius:** Per-match render time.
- **Mitigation:**
  1. **Per-render output size cap** (default 16 KB per rendered query string). Truncation triggers a refusal verdict (`ReasonTemplateTooLarge`).
  2. Template helper namespace excludes recursion-heavy helpers.
  3. Inventory's per-metric label count is already bounded by Prometheus discovery (typically <30 labels).
- **Residual risk:** Low.
- **Adversary test:** `testdata/adversarial/render_size_blowup.yaml`.

### T7 — Match-predicate evaluation explosion

- **AS3**.
- **Description:** A predicate with deeply nested `any_of` / `all_of` / `not` combinators (e.g. 100 levels deep). Each evaluation walks the tree.
- **Blast radius:** Per-match CPU spike.
- **Mitigation:**
  1. **Predicate depth cap** (default 8 levels of nesting). CUE schema enforces via constraint counter; loader rechecks at decode.
  2. **Predicate node budget** (default 64 nodes per recipe predicate). Combined with the depth cap, evaluation is bounded.
- **Residual risk:** Very low.
- **Adversary test:** `testdata/adversarial/predicate_deep_nesting.yaml`.

### T8 — Banned-label injection via query template

- **AS6**, AS9.
- **Description:** User authors a `query_template` that hardcodes a banned label as a matcher: `sum by (user_id) (rate(...))`. Recipe is registered; on match, emits a query that violates safety.
- **Blast radius:** PII / cardinality risk if the query reaches Prometheus.
- **Mitigation:**
  1. **The 5-stage validate pipeline runs every emitted query.** Stage 4 (safety) walks the AST, refuses any banned-label-in-grouping. The recipe cannot bypass it. This is the foundational mitigation. Recipes can author hostile templates; the validator catches them.
  2. The emitted query lands in the rendered dashboard with `Verdict: Refuse, ReasonBannedLabelGrouping` — visible in `warnings.json`.
  3. (Optional, defense-in-depth) Loader-time static analysis: parse query_template as PromQL after rendering with a synthetic context, refuse any recipe whose template *can* produce banned labels for any input. Not implemented in v0.3 because the validate stage already covers it; tracked as future hardening.
- **Residual risk:** Low. The validate pipeline is the load-bearing defense.
- **Adversary test:** `testdata/adversarial/banned_label_grouping.yaml`.

### T9 — PromQL grammar abuse via template injection

- **AS6**, AS9.
- **Description:** User authors a template that, when rendered, produces invalid PromQL (mismatched parentheses, undefined functions). Goal: cause downstream parser to misbehave.
- **Blast radius:** Verdict pollution; nominally caught by stage-1 parse.
- **Mitigation:**
  1. Stage-1 parse rejects malformed PromQL deterministically. Recipe gets `ReasonParseError`; render proceeds with a refusal panel.
  2. Loader does not attempt to parse rendered queries at load time (would require synthetic match contexts and is fragile). Defers to validate.
- **Residual risk:** Low.
- **Adversary test:** `testdata/adversarial/promql_grammar_abuse.yaml`.

### T10 — Determinism violation via map iteration

- **AS6**.
- **Description:** A template's `{{ range $k, $v := .Labels }}` iterates over a map. Naive Go map iteration is unordered.
- **Blast radius:** Goldens flake; CI noise; users report "intermittent test failures."
- **Mitigation:**
  1. **`text/template` sorts map keys before iteration** (stdlib documented behavior). This is the primary defense.
  2. RenderContext.Labels is a `map[string]string`, but `RenderContext.LabelList` is the sorted slice; the helper namespace prefers `LabelList` for iteration.
  3. Determinism test (`TestRecipesDSL_Determinism`) runs the full pipeline twice and asserts byte-equality; catches regressions.
- **Residual risk:** Very low.
- **Adversary test:** `testdata/adversarial/template_map_iteration.yaml` + `TestRecipesDSL_Determinism`.

### T11 — Symlink escape via --recipes-dir

- **AS8**.
- **Description:** User points `--recipes-dir /tmp/safe`. Inside, a symlink `/tmp/safe/escape.yaml -> /etc/shadow`. Loader reads `/etc/shadow` as if it were a recipe.
- **Blast radius:** Information disclosure (loader logs the file content on parse error).
- **Mitigation:**
  1. **Symlinks pointing outside `--recipes-dir` (resolved against the canonicalized dir root) are rejected during walk.** Implementation: `filepath.EvalSymlinks(target)` + check that `strings.HasPrefix(resolved, root)`.
  2. The yaml.v3 parser's error messages are sanitized at the loader boundary — file paths are reduced to basename when surfaced via dashgen's logger, but parse errors include the full file path so debug context remains.
  3. The loader explicitly does NOT log file contents on parse error (only line+col + message excerpt).
- **Residual risk:** Low. (The user is running dashgen as themselves; they could read /etc/shadow directly. The risk is when dashgen is wrapped by automation that grants extra privileges; that's an automation concern.)
- **Adversary test:** `testdata/adversarial/symlink_escape/` (directory with a symlink fixture; test creates symlink at runtime since git can't store one cross-platform).

### T12 — Override of built-in recipe with malicious shadow

- **AS3**, AS6.
- **Description:** User drops a YAML in `--recipes-dir` with `name: service_http_rate`. By precedence rules, this shadows the built-in recipe. Adversarial intent: subtly mutate output without the user noticing.
- **Blast radius:** User's dashboards differ from built-in expectations; trust violated.
- **Mitigation:**
  1. **Override is logged at WARN level** with both file paths (built-in source + user override path). The user sees the override every run.
  2. `dashgen recipe list --source user` shows all user recipes including overrides.
  3. `dashgen recipe diff --against-builtin <name>` (proposed for `RECIPES-CLI.md`) renders a side-by-side comparison.
  4. The override is the user's deliberate action via their config dir — this is intended behavior, not a vulnerability. Mitigation is *visibility*, not prevention.
- **Residual risk:** Acceptable by design. Override is a feature.
- **Adversary test:** `testdata/adversarial/shadow_override.yaml` + `loader_test.go::TestRegister_OverrideEmitsWarning`.

### T13 — Resource exhaustion via recipe-fan-out per metric

- **AS3**, AS6.
- **Description:** Recipe matches a very common metric type (e.g. `type: counter`) and emits 100 panel templates. For an inventory with 1000 counters, that's 100k panels — overwhelms render and Prometheus.
- **Blast radius:** Generate hangs, Prometheus rate-limited, validate budget exhausted.
- **Mitigation:**
  1. **Existing per-run validate budget** (default 200 calls). Excess panels are refused with `ReasonBudgetExhausted`. Output is bounded.
  2. **Per-recipe panel cap** (`panels: list.MaxItems(16)` in CUE schema). A recipe declaring more than 16 panels is rejected at load.
  3. **Profile-level panel cap** (existing): synth's `SynthesizeWithCap` enforces per-profile maxima.
- **Residual risk:** Low. Multiple layers of bounding.
- **Adversary test:** `testdata/adversarial/panel_fan_out.yaml` + verify validate budget refuses panels.

### T14 — Confidence-score gaming

- **AS3**.
- **Description:** User sets `confidence: 1.0` to force their override to win over a built-in with `confidence: 0.85` in panel-cap tiebreak.
- **Blast radius:** User's recipe wins panel slots over built-ins on the same metric.
- **Mitigation:**
  1. By design, this is fine: user explicitly opts into their override winning. The user is shaping their own dashboard.
  2. CUE schema bounds confidence to [0.0, 1.0]; `>1.0` is rejected.
  3. The panel-cap tiebreak is `(confidence desc, UID asc)` — the user's recipe wins by intent. Visibility (warnings on override) is the operator's safety net.
- **Residual risk:** Acceptable by design.
- **Adversary test:** none required (intended behavior).

### T15 — Helper namespace abuse

- **AS5**, AS6.
- **Description:** A future helper added to FuncMap has an unsafe surface (e.g. `getEnv("FOO")`, `readFile(path)`). User template invokes it.
- **Blast radius:** Information disclosure, hermeticity violation, determinism violation.
- **Mitigation:**
  1. **Helper namespace is closed.** No user-defined helpers. Adding a helper requires a docs PR + design review (per `RECIPES-DSL.md` §7.3).
  2. **Banned helpers documented**: `now`, `env`, `exec`, `readFile`, `httpGet`, `time` — all explicitly excluded.
  3. **Audit checklist** for new helpers (§5 below) requires:
     - Pure function (no I/O).
     - Deterministic (same input → same output forever).
     - Bounded execution (no loops over user input, or loops bounded by input size).
     - No reflection.
- **Residual risk:** Low (governed by review process, not loader-time check).
- **Adversary test:** None at runtime; tracked via review process.

### T16 — YAML schema-version downgrade

- **AS1**, AS2.
- **Description:** User authors a recipe with `apiVersion: dashgen.io/v0` (or omits the field). Loader silently accepts and applies wrong defaults.
- **Blast radius:** Behavior drift; broken recipes load as if v1.
- **Mitigation:**
  1. **CUE schema constrains `apiVersion: "dashgen.io/v1"` exactly** (no disjunction, no default). Missing or unknown apiVersion → unification fails at load, with a clear error mapped to `recipes/<file>:<line>: apiVersion must be 'dashgen.io/v1' (got '<actual>')`.
  2. Future `apiVersion: dashgen.io/v2` schema will live alongside v1; the loader dispatches by apiVersion. v1 recipes never break.
- **Residual risk:** Very low.
- **Adversary test:** `testdata/invalid/missing_apiversion.yaml` + `wrong_apiversion.yaml`.

### T17 — Profile contamination

- **AS3**.
- **Description:** User declares `profile: service` but match predicate fires on infra-shape metrics (e.g. `name_has_prefix: node_`). Recipe contaminates service dashboards with infra panels.
- **Blast radius:** Visual noise, incorrect dashboards.
- **Mitigation:**
  1. **Registry enforces profile binding.** A recipe registered as `service` is consulted ONLY during `--profile service` synth. Cross-profile contamination is structurally impossible.
  2. The user's per-profile mismatch produces panels in the wrong dashboard, but only when they pass `--profile <wrong>`. Operator visibility, not loader concern.
- **Residual risk:** Acceptable. Profile is the user's declaration; registry enforces it correctly.
- **Adversary test:** `loader_test.go::TestRegistry_ProfileBinding`.

### T18 — Embedded literal leak in query_template

- **AS6**.
- **Description:** User authors a template containing a hardcoded credential or secret. The recipe is shared (e.g. published to a contrib pack). Recipe loads cleanly; the secret is now in the user's dashboard JSON.
- **Blast radius:** Information disclosure (secret in dashboard, possibly exposed via Grafana).
- **Mitigation:**
  1. **Out of loader scope.** This is user error in writing their own template. The loader has no semantic understanding of "secret."
  2. `dashgen recipe lint` (CLI spec §3.3) optionally runs a pluggable secret-scanner pass on query/legend/title strings; emits a WARN. This is opt-in; default off.
  3. CHANGELOG note + doc warning in `docs/RECIPES-USER-GUIDE.md`: "Never embed secrets in recipe templates. Use Prometheus relabel rules for credential injection."
- **Residual risk:** Medium (depends on user behavior). Mitigation is education + opt-in scanner.
- **Adversary test:** `testdata/adversarial/embedded_secret.yaml` + opt-in secret-scanner test.

### T19 — Pair-resolution name collision attack

- **AS7**.
- **Description:** Adversary recipe declares `pair_with: { explicit: { name: "kube_pod_status_phase" } }`. The resolver returns the well-known kube-state metric as the pair, regardless of context. The resulting query mixes two unrelated metrics.
- **Blast radius:** Nonsensical query; refused by validate; visible in warnings.
- **Mitigation:**
  1. Validate's stage-1 parse + stage-2 selector accept the query (it's syntactically valid, the metrics exist). Stage-3 execute returns whatever the backend returns. There's no semantic check that two metrics "belong" together.
  2. **Loader does NOT attempt semantic pair-validity checks.** The user is responsible for declaring sensible pairs. `dashgen recipe explain` can be used to debug.
  3. `dashgen recipe diff --against-builtin` shows the user the differential effect.
- **Residual risk:** Low (semantically nonsense; visible in dashboard; not a security issue).
- **Adversary test:** None required (acceptable user error).

### T20 — Implicit unicode normalization

- **AS1**, AS3, AS6.
- **Description:** User authors `name_equals: "service_http_rate"` but the file contains a Cyrillic 'е' (U+0435) instead of Latin 'e' (U+0065). The recipe's name doesn't match any real metric; debugging is hard.
- **Blast radius:** Confusing user experience; "my recipe doesn't fire" issues that are hard to triage.
- **Mitigation:**
  1. **CUE schema constrains name-shaped fields to ASCII** via regex constraint `=~"^[\\x00-\\x7f]+$"` (Prometheus metric names are ASCII per the exposition format spec).
  2. `dashgen recipe lint` emits an explicit error: `recipes/<file>:<line>: name_equals contains non-ASCII character '<char>' at offset <n>`.
  3. Same constraint on `name_has_prefix`, `name_has_suffix`, `name_contains`, `name_contains_any`, `name_equals_any` items, `pair_with.suffix_swap.{from,to}_suffix`, `pair_with.prefix_swap.{from,to}_prefix`, `pair_with.explicit.name`.
- **Residual risk:** Very low.
- **Adversary test:** `testdata/adversarial/unicode_homograph.yaml`.

---

## 3. Invariants Enforced

After load, the following invariants hold and the test suite enforces them:

| ID | Invariant | Enforcement |
|---|---|---|
| I1 | Every emitted query passes through the 5-stage validate pipeline. | `internal/app/generate/generate.go` calls `validate.Pipeline.Validate` for every QueryCandidate. No bypass possible from a recipe. |
| I2 | Recipes never produce label *values* in their match predicate or template input. | Match predicate language excludes `label_equals` (DSL §6.4). RenderContext.Labels only has names. |
| I3 | Templates never invoke `{{ define }}` / `{{ template }}` / `{{ block }}`. | AST walk after parse rejects these node types. |
| I4 | Templates never reference undefined dot-context fields. | `text/template`'s `Option("missingkey=error")` is set. |
| I5 | Templates never call helpers not registered in FuncMap. | text/template's parser raises "undefined function" at parse, before render. Loader fails fast. |
| I6 | Match predicate depth ≤ 8 nodes, total node count ≤ 64. | Loader walks predicate AST after decode and counts; rejects on exceedance. |
| I7 | Per-file size ≤ 64 KB; per-dir count ≤ 1024; total budget ≤ 4 MB. | Discovery layer; reject before parse. |
| I8 | Per-file CUE evaluation ≤ 5 seconds. | `context.WithTimeout` on cue.Context. |
| I9 | Per-render rendered output ≤ 16 KB. | `bytes.Buffer` size check after Execute. |
| I10 | Symlinks within `--recipes-dir` resolve only to paths under the dir's canonical root. | `filepath.EvalSymlinks` + prefix check. |
| I11 | Determinism: same inventory + same recipe set ⇒ byte-identical output. | `TestRecipesDSL_Determinism` runs full pipeline twice. |
| I12 | Override warnings emit at load time when user shadows a built-in. | `loader_test.go::TestRegister_OverrideEmitsWarning`. |
| I13 | Profile binding is enforced — service recipes don't leak to infra dashboards. | `loader_test.go::TestRegistry_ProfileBinding`. |
| I14 | apiVersion is exactly `dashgen.io/v1`. | CUE schema constraint. |
| I15 | Metric-name fields (name_equals etc.) are ASCII-only. | CUE schema constraint. |

These invariants are tested in `internal/recipes/loader_test.go`, `matcher_test.go`, `template_test.go`, and `pair_test.go`.

---

## 4. Adversary Test Corpus

The corpus lives at `internal/recipes/testdata/adversarial/`. Each file is paired with a test in `loader_test.go` or `matcher_test.go` that asserts the expected behavior (rejection, contained execution, or specific verdict).

| File | Asserts |
|---|---|
| `yaml_billion_laughs.yaml` | Loader rejects with `recipes/...: file size exceeds 64 KB` (file inflated to 65 KB before parse). Or, if under cap, yaml.v3's anchor depth limit triggers with mapped error. |
| `yaml_recursive_anchor.yaml` | Same, recursive alias case. |
| `cue_eval_pathological.yaml` | Loader times out at 5s with `recipes/...: schema evaluation exceeded deadline`. |
| `redos_pattern.yaml` | Recipe loads (RE2 doesn't ReDoS); benchmark asserts O(n) on input length. |
| `template_define_directive.yaml` | Loader rejects with `recipes/...: template uses forbidden directive '{{ define }}'`. |
| `template_template_directive.yaml` | Same for `{{ template }}`. |
| `template_block_directive.yaml` | Same for `{{ block }}`. |
| `template_nested_if.yaml` | Loader rejects when AST node count > 256: `recipes/...: template has 350 AST nodes (max 256)`. |
| `render_size_blowup.yaml` | Render emits a refused candidate with `ReasonTemplateTooLarge`. |
| `predicate_deep_nesting.yaml` | Loader rejects: `recipes/...: predicate nesting depth 12 exceeds limit 8`. |
| `predicate_node_explosion.yaml` | Loader rejects: `recipes/...: predicate has 80 nodes (max 64)`. |
| `banned_label_grouping.yaml` | Recipe loads. Generated query gets `Verdict: Refuse, Reason: banned_label_grouping` from validate stage 4. Visible in `warnings.json`. |
| `promql_grammar_abuse.yaml` | Recipe loads. Validate stage 1 (parse) refuses with `ReasonParseError`. |
| `template_map_iteration.yaml` | `TestRecipesDSL_Determinism` runs the full pipeline twice; output is byte-equal. |
| `symlink_escape/` | Test creates symlink at runtime; loader rejects with `recipes/...: symlink target outside dir`. |
| `shadow_override.yaml` | Loader emits WARN with both paths (built-in + user). Override is applied. |
| `panel_fan_out.yaml` | Loader rejects: `recipes/...: panels has 32 items (max 16)`. |
| `embedded_secret.yaml` | Loader accepts (pure data); opt-in secret-scanner test asserts a WARN is emitted with the offending field name + match offset. |
| `unicode_homograph.yaml` | Loader rejects: `recipes/...: name_equals contains non-ASCII character 'е' (U+0435) at offset 8`. |
| `missing_apiversion.yaml` | Loader rejects: `recipes/...: missing required field 'apiVersion'`. |
| `wrong_apiversion.yaml` | Loader rejects: `recipes/...: apiVersion must be 'dashgen.io/v1' (got 'dashgen.io/v0')`. |

Each corpus file ships with a one-line comment at the top documenting what it tests:

```yaml
# adversary: T7 — predicate nesting depth (loader must reject)
apiVersion: dashgen.io/v1
kind: Recipe
...
```

---

## 5. Audit Checklist for Recipe Pack Reviewers

When reviewing a third-party recipe pack (e.g. an open-source contrib pack from a vendor), reviewers walk this checklist:

### 5.1 Schema compliance

- [ ] All `*.yaml` pass `dashgen recipe lint` with exit 0.
- [ ] No file exceeds 64 KB.
- [ ] No recipe declares `panels` with more than 16 entries.
- [ ] No recipe declares predicate nesting depth > 8.

### 5.2 Match predicates

- [ ] Every recipe's `match` block fires on identifiable metric shapes (no overly broad `type: counter` matches without further constraints).
- [ ] No `name_matches` regex with unbounded alternation that could match thousands of metrics.
- [ ] Trait predicates use the documented set (`service_http`, `service_grpc`, `latency_histogram`).
- [ ] Profile binding makes sense: `profile: service` recipes don't match `node_*` or `kube_*` metrics.

### 5.3 Templates

- [ ] No hardcoded literals that look like credentials, hostnames of internal systems, or anything organization-specific that shouldn't ship.
- [ ] No use of forbidden directives (`{{ define }}`, `{{ template }}`, `{{ block }}` — the loader rejects these but reviewers should also flag them).
- [ ] Helpers used are all in the documented FuncMap.
- [ ] PromQL emitted is canonical (no inline secrets, no banned labels in grouping, no obvious cardinality risks).
- [ ] Quantile fan-out (`quantiles: [...]`) doesn't exceed 5 entries.

### 5.4 Pairs

- [ ] `pair_with` references plausible sibling metrics.
- [ ] `on_missing` is appropriate: `omit` for graceful degradation; `warn` only when missing pair is itself a finding.

### 5.5 Determinism

- [ ] Reviewer runs `dashgen recipe test <file> --fixture <fixture>` twice; output is byte-identical.

### 5.6 Documentation

- [ ] `description` field is present and informative.
- [ ] `tags` field present; uses standard tag vocabulary.

---

## 6. Out-of-Scope Threats (Non-Goals)

The DSL deliberately does not protect against:

- A user who has root on their own machine and decides to author a recipe that emits a query embedding their own credentials. (Out of scope; user chose to do this; no isolation possible at the loader.)
- A user who installs a malicious dashgen binary (supply chain).
- A user who runs `dashgen` with `--recipes-dir /path/with/world-writable/recipes/`. (Filesystem permission concern outside the loader.)
- An adversary with write access to `internal/recipes/data/` in the dashgen source tree. (Pre-build tampering; CI/CD signing is the answer.)
- Recipe-level supply chain attacks via shared "recipe repositories" (proposed for v0.4+; out of scope for v0.3 spec).

These are documented as non-goals so the loader's scope is well-bounded.

---

## 7. Threat Severity Matrix

| Threat | Likelihood | Impact | Mitigation Confidence | Residual |
|---|---|---|---|---|
| T1 YAML billion-laughs | Low | Med | High | Low |
| T2 File count exhaustion | Low | Med | High | Low |
| T3 CUE eval | Med | Med | Med | Med |
| T4 ReDoS | Low | Low | Very High (RE2) | Very Low |
| T5 Template parse bomb | Low | Med | High | Low |
| T6 Render-time DoS | Low | Low | High | Low |
| T7 Predicate explosion | Low | Low | High | Very Low |
| T8 Banned-label injection | Med | High | High (validate pipeline) | Low |
| T9 PromQL grammar abuse | Low | Low | High | Low |
| T10 Determinism violation | Med | Low | High | Very Low |
| T11 Symlink escape | Low | Med | Med | Low |
| T12 Built-in override | High | Low | Med (visibility) | Acceptable |
| T13 Panel fan-out | Med | Med | High | Low |
| T14 Confidence gaming | Med | Low | n/a (intended) | Acceptable |
| T15 Helper namespace | Low | High | Med (review process) | Med |
| T16 apiVersion downgrade | Low | Low | High | Very Low |
| T17 Profile contamination | Med | Low | High | Acceptable |
| T18 Embedded secret | Med | High | Low (user-error) | Med |
| T19 Pair collision | Low | Low | n/a (semantic) | Low |
| T20 Unicode homograph | Low | Low | High | Very Low |

The three threats with **Med residual risk** are T3 (CUE eval performance), T15 (helper namespace evolution), and T18 (user-authored secrets). All three are handled by process controls (CUE version pinning + benchmarks; helper review checklist; opt-in secret scanner + docs) rather than runtime mitigation alone.

---

## Document History

| Date | Author | Change |
|---|---|---|
| 2026-04-27 | initial draft | Threat model + 20 threats + invariants + adversary corpus + reviewer checklist. Companion to `RECIPES-DSL.md`. |
