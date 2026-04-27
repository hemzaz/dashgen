# DashGen Recipe Authoring CLI — Specification

> Status: DRAFT (2026-04-27). Companion to [`RECIPES-DSL.md`](RECIPES-DSL.md)
> and [`RECIPES-DSL-ADVERSARY.md`](RECIPES-DSL-ADVERSARY.md).
> Specifies the `dashgen recipe ...` subcommand surface for maintaining,
> creating, linting, and testing recipes.

---

## 0. TL;DR

`dashgen recipe` is a developer-experience subcommand group focused on the *authoring lifecycle* of YAML recipes. It is distinct from `dashgen generate` (which renders dashboards) and `dashgen lint` (which audits *rendered* dashboard bundles). The recipe subcommands operate on `*.yaml` recipe files — built-in or user-authored.

Eight subcommands cover the full lifecycle:

| Verb | What |
|---|---|
| `init` | Scaffold the user recipes directory at `$XDG_CONFIG_HOME/dashgen/recipes/` with a README and example recipe. |
| `scaffold` | Generate a starter `*.yaml` for a given metric/type/section/profile. |
| `lint` | Validate one or more recipe files against the schema, no rendering. |
| `list` | Print all registered recipes (built-in + user) with provenance. |
| `show` | Print the resolved YAML for one named recipe (after schema unification + defaults). |
| `test` | Run a recipe against a fixture: reports which metrics matched and what panels were produced, without writing dashboards. |
| `explain` | For a given recipe + metric, walk the match predicate evaluation step-by-step; show why match returned true or false. |
| `diff` | Compare two recipe files (or a user recipe vs the built-in it shadows) by their effect on a fixture. |

All subcommands are pure: no network, no Prometheus calls, no AI enrichment, no mutation of the user's filesystem outside the explicit `--output` argument they pass. Determinism is preserved.

---

## 1. Goals & Non-Goals

### Goals

| ID | Goal |
|----|------|
| G1 | A platform engineer can go from "I have a custom metric" to "I have a working recipe in my config dir" using only `dashgen recipe scaffold` + an editor + `dashgen recipe test` + `dashgen recipe lint`. No Go toolchain, no fork. |
| G2 | Every error a user could encounter (schema violation, template syntax error, regex error, missing field) is surfaced as a positional message: `<file>:<line>:<col>: <human-readable cause>`. No CUE-jargon errors leak through. |
| G3 | `dashgen recipe lint` is fast enough to be a pre-commit hook (target: ≤200ms cold, ≤50ms warm for a single recipe). |
| G4 | All subcommands are scriptable: deterministic exit codes, optional `--output json` for machine consumption. |
| G5 | `dashgen recipe explain` makes "why didn't my recipe fire?" debuggable in one command. |
| G6 | `dashgen recipe diff` makes "what does my override change?" answerable without running `dashgen generate` against a real Prometheus. |
| G7 | The CLI itself never modifies the user's recipe files. Edits are user-driven via `$EDITOR`; the CLI only reads + writes through explicit `--output` paths. |
| G8 | Subcommands are composable in shell: `dashgen recipe list --output json | jq ...` works; `find . -name '*.yaml' | xargs dashgen recipe lint` works. |

### Non-Goals

| ID | Non-Goal |
|----|----------|
| NG1 | No interactive TUI. Scaffold is one shot from flags; lint/test are batch. |
| NG2 | No live Prometheus connection. `dashgen recipe test` runs against fixtures only. (Live testing is what `dashgen generate --prom-url ...` is for.) |
| NG3 | No subcommand modifies recipe YAML in place. Format-on-save is delegated to standard YAML formatters (yamllint, prettier, etc.). |
| NG4 | No subcommand publishes recipes to a registry. There is no recipe registry yet (proposed for v0.4+). |
| NG5 | No subcommand runs the AI enrichment seam. Recipe authoring is deterministic-only. |
| NG6 | No support for Go-format recipes via this CLI. The CLI operates on YAML wire format (per `RECIPES-DSL.md`). |
| NG7 | No nested subcommands beyond the eight listed. The surface is flat by design. |

---

## 2. Subcommand Catalog

```
dashgen recipe init       [--config-dir <path>] [--force]
dashgen recipe scaffold   --metric <name> --type <type> --section <section> --profile <profile>
                          [--name <recipe-name>] [--confidence <float>] [--output <path>] [--with-pair <suffix-swap-spec>]
dashgen recipe lint       <file...> [--strict] [--output text|json] [--secret-scan]
dashgen recipe list       [--profile <profile>] [--source builtin|user|all] [--output text|json]
                          [--match <name-glob>]
dashgen recipe show       <name> [--profile <profile>] [--output yaml|json|tree]
                          [--source builtin|user|all]
dashgen recipe test       <file>... --fixture <fixture-dir> [--output text|json] [--verbose]
                          [--metric <name>] [--profile <profile>]
dashgen recipe explain    --name <recipe> --metric <name> --fixture <fixture-dir>
                          [--output text|json] [--profile <profile>]
dashgen recipe diff       <fileA> <fileB> --fixture <fixture-dir> [--output text|json|unified]
                          [--against-builtin]
```

Conventions across all subcommands:
- Positional arguments precede flags.
- `--output` controls format: `text` (default, human-readable), `json` (machine-readable), `yaml` / `tree` / `unified` where contextually appropriate.
- `--verbose` adds debug logging.
- `--quiet` suppresses non-error output.
- Exit codes per §6.

---

## 3. Per-Subcommand Specifications

### 3.1 `dashgen recipe init`

**Purpose:** Bootstrap the user recipes directory.

**Behavior:**
1. Resolve `$XDG_CONFIG_HOME/dashgen/recipes/` (fallback `$HOME/.config/dashgen/recipes/`). Override via `--config-dir`.
2. Create the directory if missing (mode 0755).
3. Write three files inside:
   - `README.md` — pointer to `docs/RECIPES-USER-GUIDE.md`, the schema URL, common pitfalls.
   - `example.yaml` — a fully-commented example recipe (Tier-A pattern), valid against the schema.
   - `.gitignore` — pre-populated with common patterns (`*.bak`, `*.tmp`, `.DS_Store`).
4. Print the absolute path of the created directory and a one-liner: `Recipes directory ready. Drop *.yaml files here, then run 'dashgen generate' to use them.`

**Flags:**
- `--config-dir <path>`: override default location. Useful for testing or for users who want a non-XDG path.
- `--force`: if the directory already exists, overwrite the three scaffolded files. By default, refuses if files exist.

**Exit codes:** 0 on success, 1 on filesystem error, 2 on existing-directory-without-`--force`.

**Adversary considerations:** see §9 CT1, CT4.

### 3.2 `dashgen recipe scaffold`

**Purpose:** Generate a starter recipe YAML from metric metadata.

**Behavior:**
1. Validate flag combinations:
   - `--metric` is required (Prometheus metric name; ASCII; matches `^[a-zA-Z_:][a-zA-Z0-9_:]*$`).
   - `--type` is required (one of `counter`, `gauge`, `histogram`, `summary`).
   - `--section` is required (one of the canonical sections, see CUE schema).
   - `--profile` is required (`service`, `infra`, `k8s`).
2. Pick a recipe shape based on `--type`:
   - `counter` → rate-by-labels Tier-A pattern.
   - `gauge` → max-by-instance Tier-A pattern.
   - `histogram` → histogram_quantile trio (p50/p95/p99) Tier-B pattern.
   - `summary` → max-by-quantile Tier-B pattern.
3. If `--with-pair "<from>↔<to>"` is set, add a `pair_with: suffix_swap` block.
4. Render the chosen template with the provided arguments substituted in.
5. Write to `--output` (default: stdout).

**Output:** A YAML file with extensive `# ` comments documenting each section. The output passes `dashgen recipe lint` immediately.

**Flags:**
- `--metric`: required.
- `--type`: required, enum.
- `--section`: required, enum.
- `--profile`: required, enum.
- `--name`: optional, defaults to `--metric` value with non-`[a-z0-9_]` runes mapped to `_`.
- `--confidence`: optional, default 0.85.
- `--with-pair "<from>↔<to>"`: optional. e.g. `--with-pair "_size_bytes↔_avail_bytes"`.
- `--output <path>`: optional, default stdout. Refuses to overwrite existing files unless `--force`.
- `--force`: allow overwrite of `--output`.

**Exit codes:** 0 on success, 1 on validation error (bad flag combo), 2 on output write error.

**Example:**
```
$ dashgen recipe scaffold \
    --metric mycorp_queue_depth \
    --type gauge \
    --section saturation \
    --profile service \
    --output ~/.config/dashgen/recipes/mycorp_queue_depth.yaml

Wrote: /home/alice/.config/dashgen/recipes/mycorp_queue_depth.yaml
Next: $EDITOR /home/alice/.config/dashgen/recipes/mycorp_queue_depth.yaml
      dashgen recipe lint /home/alice/.config/dashgen/recipes/mycorp_queue_depth.yaml
      dashgen recipe test /home/alice/.config/dashgen/recipes/mycorp_queue_depth.yaml --fixture testdata/fixtures/service-realistic
```

### 3.3 `dashgen recipe lint`

**Purpose:** Validate one or more recipe YAML files against the schema, without running generate.

**Behavior:**
1. Accept `<file...>` positional args. Globs are NOT expanded by the CLI (shell expands them). For multi-file lint, the user runs `find . -name '*.yaml' | xargs dashgen recipe lint`.
2. For each file:
   - Read bytes, enforce the loader's per-file size cap (64 KB).
   - Run the loader pipeline (DSL §9.2) up through step 9 (validate template helpers + regex compilation). Skip step 10 (registration into the registry).
   - Collect all errors with positions.
3. Render results per `--output`:
   - `text` (default): `<file>: OK` on success, `<file>:<line>:<col>: <error>` on failure. Multi-error output is grouped per file.
   - `json`: `[ { file, valid, errors: [{line, col, code, message}] }, ... ]`.
4. Exit 0 iff every file is valid.

**Flags:**
- `--strict`: in addition to schema errors, emit warnings as errors (e.g. non-canonical units, unanchored regex). Default off.
- `--secret-scan`: run the opt-in secret scanner (per `RECIPES-DSL-ADVERSARY.md` T18) over query/legend/title strings. Emits findings as warnings (or errors if `--strict`).
- `--output text|json`: default text.
- `--quiet`: suppress per-file `OK` lines; only print failures.

**Exit codes:** 0 on all-valid, 1 on any invalid, 2 on filesystem error (file not found, permission denied).

**Performance target:** ≤200ms cold (CUE compile + 1 file), ≤50ms warm (subsequent files reuse the compiled schema).

**Example:**
```
$ dashgen recipe lint mycorp_queue_depth.yaml
mycorp_queue_depth.yaml: OK

$ dashgen recipe lint broken.yaml
broken.yaml:5:3: section must be one of [overview traffic errors latency saturation cpu memory disk network pods workloads resources]; got 'satturation'
broken.yaml:12:5: query_template references undefined helper 'doesNotExist'
exit status 1
```

### 3.4 `dashgen recipe list`

**Purpose:** Print all registered recipes, with provenance.

**Behavior:**
1. Run the loader's discovery + load pipeline. This includes built-in recipes (embedded `data/**/*.yaml`) and user recipes (from `--recipes-dir` flag or XDG default).
2. Apply filters: `--profile`, `--source`, `--match`.
3. Emit per `--output`:
   - `text`: tabular columns `NAME | PROFILE | SECTION | CONFIDENCE | SOURCE | PATH`. Sorted by `(profile, name)`.
   - `json`: array of objects with the same fields plus `tier`, `tags`, `description`.

**Flags:**
- `--profile <profile>`: filter to one profile.
- `--source builtin|user|all`: filter by load source. Default `all`.
- `--match <glob>`: shell-style glob against name. e.g. `--match 'service_*'`.
- `--output text|json`: default text.
- `--recipes-dir <path>`: same as `dashgen generate`. Repeatable.
- `--no-user-recipes`: ignore user dirs. Builtins only.

**Exit codes:** 0 on success, 1 on load error (e.g. invalid recipe in dir, since `list` triggers full load).

**Example:**
```
$ dashgen recipe list --profile service --source user
NAME                  PROFILE  SECTION     CONFIDENCE  SOURCE  PATH
mycorp_lag_seconds    service  latency     0.85        user    /home/alice/.config/dashgen/recipes/mycorp_lag_seconds.yaml
mycorp_queue_depth    service  saturation  0.85        user    /home/alice/.config/dashgen/recipes/mycorp_queue_depth.yaml

$ dashgen recipe list --match 'service_http_*' --output json | jq '.[].name'
"service_http_errors"
"service_http_latency"
"service_http_rate"
```

### 3.5 `dashgen recipe show`

**Purpose:** Print the resolved (post-unification, post-defaults) YAML for one named recipe.

**Behavior:**
1. Run loader full pipeline.
2. Look up by name. If `--profile` is omitted and the name exists in multiple profiles, error with disambiguation prompt.
3. Render:
   - `yaml` (default): the canonical YAML with all defaults applied (e.g. `kind: timeseries` filled in). Useful for "what's the runtime view of my recipe?"
   - `json`: the same as a JSON value (CUE's canonical encoding).
   - `tree`: an indented tree view, helpful for visual inspection.

**Flags:**
- `--profile <profile>`: disambiguator if cross-profile name collision.
- `--output yaml|json|tree`: default yaml.
- `--source builtin|user|all`: default all. If the recipe exists in both, the user override wins (matching runtime), but `--source builtin` lets you see the original.
- `--recipes-dir`, `--no-user-recipes`: as in §3.4.

**Exit codes:** 0 on found, 1 on not-found, 2 on disambiguation needed.

**Example:**
```
$ dashgen recipe show service_http_rate --output yaml
apiVersion: dashgen.io/v1
kind: Recipe
metadata:
  name: service_http_rate
  ...
match:
  type: counter
  any_trait: [service_http]
panels:
  - title_template: "HTTP request rate"
    kind: timeseries        # default applied
    unit: ops/sec
    ...
```

### 3.6 `dashgen recipe test`

**Purpose:** Run a recipe (or set of recipes) against a fixture, report match outcomes and panel structure, without writing a dashboard.

**Behavior:**
1. Load the recipe file(s) — same loader pipeline as `lint`, but without registry side-effects (recipes here are "tested in isolation").
2. Load the fixture's inventory (metadata + series) — same shape as `dashgen generate --in <fixture>`.
3. Classify the inventory using the existing classifier.
4. For each loaded recipe:
   a. Walk every metric in the inventory; record which metrics matched.
   b. For each match, build panels (run the template engine with the synth machinery's render context).
   c. Send the rendered queries through the 5-stage validate pipeline (using fixture data; no real Prometheus).
5. Report:
   - For each recipe: list matched metrics, list emitted panels with verdicts, list refusals + reasons.
   - For each non-matched metric the recipe specifically targets (e.g. metric in `name_equals` list), explain why the match failed.

**Flags:**
- `<file>...`: positional, one or more recipe YAML files.
- `--fixture <fixture-dir>`: required, path to a fixture under `testdata/fixtures/<name>/`.
- `--metric <name>`: optional filter; only test against this metric. Useful for "does this recipe fire on this one metric?"
- `--profile <profile>`: override the recipe's declared profile (advanced; for cross-profile testing).
- `--output text|json`: default text.
- `--verbose`: include the full rendered query string per panel; otherwise truncated.

**Output (text):**
```
$ dashgen recipe test mycorp_queue_depth.yaml --fixture testdata/fixtures/service-realistic
Recipe: mycorp_queue_depth (profile=service)
Matched metrics: 1
  - mycorp_queue_depth (gauge)
Panels: 1
  [Verdict: Accept] Queue depth by queue, topic
    query: max by (job, instance, queue, topic) (mycorp_queue_depth{ job=~".+", instance=~".+" })

Recipe: (deeper diagnostics)
  Predicate evaluation per metric (top 5 non-matches):
    mycorp_request_total: name_equals expected 'mycorp_queue_depth', got 'mycorp_request_total' (false)
    ...
```

**Output (json):** structured with `recipe`, `matches: [{metric, panels: [...]}]`, `non_matches: [{metric, predicate_trace}]`.

**Exit codes:** 0 on success (recipe loaded + tested, no validate refusals OR refusals are documented), 1 on load error, 2 on fixture error (fixture not found / malformed), 3 on `--strict` mode if any validate refusal occurred.

**Adversary considerations:** see §9 CT2, CT6, CT7.

### 3.7 `dashgen recipe explain`

**Purpose:** Walk the match-predicate evaluation step-by-step for one recipe + one metric. Answers "why didn't my recipe fire on metric X?"

**Behavior:**
1. Load the recipe (must be registered, not just a file — `--name` looks it up in the loaded registry).
2. Load the fixture and find the named metric.
3. Walk the recipe's match predicate AST. For each node, record:
   - Node type (primitive / any_of / all_of / not).
   - For primitives, the field, expected value, actual value.
   - The boolean result.
4. Emit a tree of evaluation:

```
Recipe: service_db_query_latency
Metric: mycorp_query_latency_seconds (histogram)

match (all_of) → ?
├── type: histogram → ✓ (metric is histogram)
├── any_trait: [latency_histogram] → ✓ (metric has latency_histogram)
├── name_contains_any: [query, db, sql] → ✓ ('mycorp_query_latency_seconds' contains 'query')
└── none_trait: [service_http, service_grpc] → ✗ (metric has trait 'service_http')

RESULT: false (the 'none_trait' check excluded this metric because it has the service_http trait)
```

This is the load-bearing debugging tool. Without it, "my recipe doesn't fire" is hard to diagnose.

**Flags:**
- `--name <recipe>`: required. Must be a registered recipe (run `list` to see options).
- `--metric <name>`: required. Must exist in the fixture.
- `--fixture <fixture-dir>`: required.
- `--profile <profile>`: optional disambiguator.
- `--output text|json`: default text.

**Output (json):** A nested object representing the evaluation tree, with `node`, `type`, `result`, `expected`, `actual` fields.

**Exit codes:** 0 always (this is a diagnostic, not a pass/fail). Errors (recipe-not-found, metric-not-found) → exit 2.

### 3.8 `dashgen recipe diff`

**Purpose:** Compare two recipe files (or a user override vs the built-in it shadows) by their effect on a fixture.

**Behavior:**
1. Load `<fileA>` and `<fileB>` (or fileA + the built-in matching its name when `--against-builtin` is set).
2. Load the fixture.
3. Run both recipes through the synth pipeline against the fixture (as in `dashgen recipe test`).
4. Compare resulting panel structures: for each `(metric, panel-kind, section)` tuple, show:
   - Panels added by B (not present in A).
   - Panels removed by B (in A but not B).
   - Panels changed (same identity but differing query/legend/title/unit/group_by).
5. Render per `--output`:
   - `text` (default): grouped human-readable summary.
   - `unified`: unified diff of the rendered query strings (similar to `git diff`).
   - `json`: structured array of changes.

**Flags:**
- `<fileA>`, `<fileB>`: positional. If `--against-builtin` is set, omit `<fileB>` and the CLI looks up the built-in by name from `<fileA>`.
- `--fixture <fixture-dir>`: required.
- `--against-builtin`: compare `<fileA>` against the built-in with the same name.
- `--output text|json|unified`: default text.

**Output (text):**
```
$ dashgen recipe diff service_http_rate.user.yaml --against-builtin --fixture testdata/fixtures/service-realistic
Recipe: service_http_rate
Built-in: internal/recipes/data/service/service_http_rate.yaml
Override: ./service_http_rate.user.yaml

Panels changed:
  [HTTP request rate]
    query:
      - sum by (job, instance, method, route, status_code) (rate(http_requests_total{ ... }[5m]))
      + sum by (job, instance, method, route, status_code, region) (rate(http_requests_total{ ... }[5m]))
    legend:
      (no change)
    group_by:
      - [method, route, status_code]
      + [method, route, status_code, region]

Panels added:    0
Panels removed:  0
Panels changed:  1
```

**Exit codes:** 0 if no differences, 1 if differences exist (so the command can be used as a CI gate).

**Adversary considerations:** see §9 CT6.

---

## 4. Common Flags & Conventions

### 4.1 Flag conventions

- All flags use `--kebab-case`.
- Boolean flags default to false; `--flag` sets to true.
- Path flags: relative paths resolve against `cwd`; tilde expansion is handled by the shell, not the CLI.
- File-list positional args: globs are shell-expanded; the CLI does not expand globs.

### 4.2 Global flags inherited from cobra root

- `--quiet`: suppress non-error output.
- `--verbose`: enable debug logging.
- `--no-color`: disable ANSI color in text output (default: auto-detect TTY).

### 4.3 `--recipes-dir` semantics

Same as `dashgen generate`. Repeatable; later wins on collision. `--no-user-recipes` ignores all user dirs (XDG + flag).

---

## 5. Output Formats

### 5.1 `text`

Default. Human-readable, color-aware (TTY only). Stable across versions for stdout-piped consumption *only at the column-name level*; the formatting itself is not a public contract.

### 5.2 `json`

Stable contract. Schema documented per subcommand. JSON is single-line per top-level entity unless the result is naturally array-shaped, in which case it's a JSON array. Streaming JSON (NDJSON) is NOT used; outputs are bounded and typically small.

### 5.3 `yaml` (for `show` only)

Canonical YAML output of the resolved recipe. This is what the loader's internal representation looks like after schema unification.

### 5.4 `unified` (for `diff` only)

Standard unified-diff format with a `---` / `+++` header pair. Suitable for piping into `colordiff`, `diff-so-fancy`, etc.

### 5.5 `tree` (for `show` and `explain` only)

ASCII tree format, similar to `tree(1)`, for visual nesting inspection.

---

## 6. Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Validation / lint failure / diff non-empty (varies by subcommand; documented per-section). |
| 2 | User-input error (bad flag combo, file not found, ambiguous name). |
| 3 | Strict-mode warning escalation (e.g. `lint --strict` with warnings). |
| 5 | Loader resource limit hit (file too large, too many files). |
| 7 | Refused-output (validate pipeline refused all queries — same code as `dashgen lint`). |
| 64 | Internal error (bug). |

Exit codes are stable: scripts can rely on them.

---

## 7. Error Mapping

All user-facing errors follow the format `<source>:<line>:<col>: <human-readable message>` where `<source>` is a file path or descriptive label (e.g. `flag --metric`).

CUE-internal error strings are translated to human-readable equivalents by the loader's error-mapping layer (per `RECIPES-DSL.md` §9.3). The CLI does not surface raw CUE errors.

For non-positional errors (e.g. flag validation), the format is `dashgen: <message>` with no position.

---

## 8. CI / Workflow Integration

### 8.1 Pre-commit hook

```bash
# .git/hooks/pre-commit
#!/bin/sh
files=$(git diff --cached --name-only --diff-filter=ACM | grep '\.yaml$' | grep -E '(recipes|\.config/dashgen)/')
[ -z "$files" ] || dashgen recipe lint $files
```

Lint is fast enough (≤50ms warm per file) that this runs cleanly on commits with many edited recipes.

### 8.2 GitHub Actions

```yaml
# .github/workflows/recipes.yml
- name: Lint recipes
  run: dashgen recipe lint $(find . -name '*.yaml' -path '*/recipes/*')

- name: Test recipes against fixtures
  run: |
    for fixture in testdata/fixtures/*/; do
      dashgen recipe test ./recipes/*.yaml --fixture "$fixture" --output json
    done
```

### 8.3 Recipe pack publishing

A vendor publishing a recipe pack runs:

```bash
dashgen recipe lint --strict --secret-scan ./pack/*.yaml
dashgen recipe test ./pack/*.yaml --fixture testdata/fixtures/service-realistic
```

Both must exit 0 before publishing.

---

## 9. Adversary Specifications (CLI surface)

This complements `RECIPES-DSL-ADVERSARY.md`, which covers loader/runtime threats. The CLI surface adds its own threats: file-system manipulation, output paths, flag injection, glob handling.

### 9.1 Threat model

The CLI runs as the user. Recipes loaded by the CLI are untrusted. The CLI's outputs (stdout, files written via `--output`) are trusted by downstream tooling (e.g. CI). Threats fall into:

- File-system manipulation via flag values.
- Output-file overwrites of unintended paths.
- Argument injection through scaffold flags.
- Resource exhaustion via large input lists.
- Information disclosure via verbose error messages.

### 9.2 Threat catalog

#### CT1 — Output-path traversal

- **Subcommands:** `init`, `scaffold`.
- **Description:** User passes `--output ../../etc/passwd` and `dashgen` writes recipe content into a sensitive path.
- **Mitigation:**
  1. **Default mode:** if `--output` exists, refuse to overwrite. Require `--force` to overwrite.
  2. The CLI does not chmod/chown written files; mode follows `os.Create`'s default (0644 modulo umask). No setuid/setgid trickery.
  3. Path is treated as user-provided; no traversal sanitization beyond the existence check. (The user is the principal; voluntary overwrite is the user's choice.)
- **Residual risk:** Acceptable. Same as any CLI's `--output` flag.

#### CT2 — Recipe-list explosion

- **Subcommands:** `lint`, `test`.
- **Description:** User runs `find / -name '*.yaml' | xargs dashgen recipe lint`. Process loads every YAML file on the system.
- **Mitigation:**
  1. Per-file size cap (64 KB) applies in the CLI as in the loader.
  2. **Per-invocation total file count cap** (default 4096). Exceeded → CLI emits a clear error and refuses to process further.
  3. Per-invocation wall-clock budget (default 60 seconds). Exceeded → graceful exit with reported partial results.
- **Residual risk:** Low.

#### CT3 — Scaffold flag injection

- **Subcommands:** `scaffold`.
- **Description:** User passes a metric name containing shell metacharacters or quote-breaking sequences. The scaffold template embeds the metric name verbatim into the YAML; what if the YAML is later piped to a shell?
- **Mitigation:**
  1. **`--metric` is constrained to Prometheus metric-name regex** `^[a-zA-Z_:][a-zA-Z0-9_:]*$` (max 128 runes). Rejected at flag parse if mismatched.
  2. Same constraint on `--name`.
  3. `--section`, `--profile`, `--type`: enum-validated.
  4. `--confidence`: numeric, 0.0–1.0.
  5. `--with-pair`: parsed as `<from>↔<to>` where both halves match `^[a-zA-Z_:][a-zA-Z0-9_:]*$`.
  6. The scaffolded YAML is itself validated by `dashgen recipe lint` before exit (an internal sanity check); if scaffold produces invalid YAML, exit code 64 (internal error).
- **Residual risk:** Very low.

#### CT4 — Symlink target via `--output`

- **Subcommands:** `init`, `scaffold`.
- **Description:** User has a symlink at `--output` pointing to a sensitive system path. CLI writes through the symlink.
- **Mitigation:**
  1. **CLI uses `os.OpenFile` with `O_EXCL` when writing.** Existing files (including symlinks) cause failure unless `--force`. With `--force`, the symlink is overwritten as a regular file (the previous symlink target is unaffected).
  2. The CLI does not call `filepath.EvalSymlinks` on `--output` paths — it trusts the user's path semantics.
- **Residual risk:** Acceptable. Same threat model as any text editor.

#### CT5 — `explain` data exfiltration

- **Subcommands:** `explain`.
- **Description:** Verbose evaluation output includes label values from the fixture. If the fixture comes from a real Prometheus snapshot, label values may be sensitive.
- **Mitigation:**
  1. **`explain` operates on label NAMES only**, not values, consistent with the DSL's data model. The output text contains label names, metric names, and the recipe's match predicates — never label values.
  2. If `--verbose`, additional debug context is included but still only names.
  3. The fixture data on disk is the user's responsibility; the CLI doesn't fetch from Prometheus.
- **Residual risk:** Very low.

#### CT6 — Diff of malicious user override

- **Subcommands:** `diff` with `--against-builtin`.
- **Description:** User runs `dashgen recipe diff malicious.yaml --against-builtin`. The `malicious.yaml` exploits a CUE eval bug or template parse bomb. The diff command must load both recipes; if `malicious.yaml` causes the loader to hang or OOM, the user's terminal is hung.
- **Mitigation:**
  1. All loader limits from `RECIPES-DSL-ADVERSARY.md` apply: file size cap, CUE deadline, template AST node budget, predicate depth cap.
  2. Per-invocation wall-clock budget (60 seconds for `diff`); exceeded → exit code 5.
- **Residual risk:** Low.

#### CT7 — Malicious fixture

- **Subcommands:** `test`, `explain`, `diff`.
- **Description:** User passes `--fixture <evil-dir>` containing a fixture with billion-laughs `metadata.json` or pathological `series.json`.
- **Mitigation:**
  1. The fixture loader uses `encoding/json` which has built-in nesting limits (10000 nested levels).
  2. **Fixture file size cap** (default 16 MB per file in the fixture, 64 MB cumulative).
  3. The fixture loader is shared with `dashgen generate --in <fixture>`; existing test coverage applies.
- **Residual risk:** Low.

#### CT8 — Information disclosure via verbose errors

- **Subcommands:** all.
- **Description:** Errors might include full paths or file content, exposing internal directory structure when the CLI is wrapped by a service.
- **Mitigation:**
  1. **Default mode:** errors include the relative path (relative to `cwd`) and never include file content beyond the offending line excerpt (max 80 columns).
  2. `--quiet` suppresses non-error output.
  3. `--verbose` enables full paths and stack traces; documented as opt-in for debugging.
- **Residual risk:** Low.

#### CT9 — Lint as DoS vector for shared CI

- **Subcommands:** `lint`.
- **Description:** A recipe pack PR contains a recipe with adversarial template that hits CT2 / DSL T3 / DSL T5 limits. The CI's `dashgen recipe lint` step takes 60+ seconds, blocking the pipeline.
- **Mitigation:**
  1. All loader limits apply — per-file deadline, AST node budget, etc.
  2. `dashgen recipe lint --output json | jq '.[] | select(.elapsed_ms > 1000)'` lets CI detect slow files.
  3. **Per-file lint budget** (default 5 seconds wall-clock per file). Exceeded → file marked invalid with `recipes/<file>: lint exceeded 5s deadline`.
- **Residual risk:** Low.

#### CT10 — Recipe scaffolder as attack vector

- **Subcommands:** `scaffold`.
- **Description:** A scripted attacker calls `dashgen recipe scaffold` repeatedly with crafted flags to attempt to exhaust disk space (writing many files via `--output`).
- **Mitigation:**
  1. The CLI does not write files unless `--output` is explicitly set (default is stdout).
  2. The CLI does not delete or modify files outside `--output`.
  3. Scaffold output is bounded (≤16 KB per recipe).
- **Residual risk:** Very low. The user is the principal.

### 9.3 Threat severity matrix

| ID | Threat | Likelihood | Impact | Mitigation Confidence | Residual |
|---|---|---|---|---|---|
| CT1 | Output-path traversal | High (user error) | Med | Med (prompt + --force) | Acceptable |
| CT2 | Recipe-list explosion | Med | Med | High | Low |
| CT3 | Scaffold flag injection | Low | Low | High | Very Low |
| CT4 | Symlink target via --output | Low | Med | Med | Acceptable |
| CT5 | explain data exfil | Low | Low | High | Very Low |
| CT6 | Diff with malicious user file | Low | Med | High | Low |
| CT7 | Malicious fixture | Low | Med | High | Low |
| CT8 | Verbose error disclosure | Med | Low | High | Low |
| CT9 | Lint DoS vector | Low | Med | High | Low |
| CT10 | Scaffolder disk DoS | Very Low | Low | High | Very Low |

### 9.4 Adversary test corpus

Lives at `cmd/dashgen/recipe/testdata/adversarial/`. Each file pairs with a test in `cmd/dashgen/recipe/<verb>_test.go`.

| Test name | File | Asserts |
|---|---|---|
| `TestScaffold_RejectsBadMetric` | `flags/bad_metric.txt` (list of bad flag values) | Scaffold exits 1 with message naming the validation rule. |
| `TestLint_RejectsTooManyFiles` | (synthetic; generates 5000 files in a tempdir) | Lint exits 5 with `recipes/...: too many files (5000 > 4096)`. |
| `TestLint_RejectsLargeFile` | `large_file.yaml` (66 KB padding) | Lint exits 5 with `recipes/large_file.yaml: file size exceeds 64 KB`. |
| `TestLint_DeadlineExceeded` | wraps `cue_eval_pathological.yaml` from DSL adversary corpus | Lint exits 5 with `recipes/...: lint exceeded 5s deadline`. |
| `TestInit_RefusesOverwrite` | (creates an existing dir) | Init exits 2 unless `--force` is passed. |
| `TestExplain_LabelNamesOnly` | (synthetic fixture with sensitive label values) | Explain output contains label names, never values. |
| `TestDiff_NoNetworkAccess` | (network-isolated test environment) | Diff completes successfully without DNS / HTTP access. |
| `TestList_HandlesUserDirSymlinkEscape` | (sets up `--recipes-dir tmp/` with symlink outside the dir) | Recipe at the symlinked path is rejected; list completes; remaining recipes load. |

---

## 10. Testing Strategy

### 10.1 Per-subcommand tests

Each subcommand has a `<verb>_test.go` file in `cmd/dashgen/recipe/`. Tests use the standard cobra-test pattern (build the root command, invoke `Execute()` with a mock writer, assert exit code and output).

Coverage targets:
- Every flag combination has at least one positive test.
- Every flag-validation error has at least one negative test.
- Every error path documented in §6 is reached by at least one test.

### 10.2 Integration tests

`cmd/dashgen/recipe/integration_test.go` runs end-to-end scenarios:

- Scaffold → write to tempdir → lint the result → test against a fixture → assert match expectations.
- Init → scaffold into the init'd dir → list shows the new recipe → show prints it.
- Diff between a builtin and a hand-edited copy → asserts non-zero exit when changed.

### 10.3 Performance tests (benchmarks)

- `BenchmarkLint_SingleFile` — target ≤200ms cold, ≤50ms warm.
- `BenchmarkList_FullRegistry` — target ≤500ms for 50 recipes.
- `BenchmarkTest_FullFixture` — target ≤2s for service-realistic.

### 10.4 Adversary tests

The corpus from §9.4 is exercised by `cmd/dashgen/recipe/adversary_test.go`.

### 10.5 Documentation tests

A test asserts that every flag documented in this spec is actually present on the cobra command (and vice versa: every cobra flag is documented). This prevents docs drift.

---

## 11. Implementation Plan

| Phase | Scope | Workers | Acceptance |
|---|---|---|---|
| **A — Skeleton** | `cmd/dashgen/recipe/` package, cobra wiring, `init` + `scaffold` (the simplest two). | 1 executor (sonnet) | Both subcommands work end-to-end on a clean machine. |
| **B — Lint + List** | `lint` (with text + JSON outputs) and `list`. Both ride the existing loader (no new infrastructure). | 1 executor (sonnet) | Pre-commit hook example works; lint hits performance target. |
| **C — Show + Test** | `show` (output yaml/json/tree) and `test` (matches + panel synthesis against a fixture). | 1 executor (opus) | `dashgen recipe test mycorp_*.yaml --fixture testdata/fixtures/service-realistic` produces the documented output. |
| **D — Explain + Diff** | The two highest-value debugging tools. Diff requires careful semantic comparison of resolved panel structures. | 1 executor (opus) + 1 test-engineer (sonnet) | "Why didn't my recipe fire?" answerable in one command; "what does my override change?" answerable in one command. |
| **E — Adversary corpus + hardening** | Implement all CT1–CT10 mitigations + adversary tests. | 1 executor (opus) + 1 security-reviewer (opus) | All adversary tests pass; performance budget enforced. |

Phases A–B can ship independently of the DSL phases (they only need the loader, not full migration). Phase C–D should ship with at least DSL Phase 1 (loader + 5 representative migrations).

---

## 12. Open Questions

These must resolve before Phase B starts:

- [ ] **JSON output stability.** Should the JSON output schemas be versioned (e.g. `version: 1` in every JSON output)? Default position: yes, but only ≥1.0 once we ship a stable contract.
- [ ] **Color output detection.** Do we use `mattn/go-isatty` or roll our own? Default: use stdlib `os.Stdout.Stat` mode-bit check + `--no-color` override.
- [ ] **`init` template content.** What does the example.yaml ship with? Proposed: a fully-commented Tier-A counter recipe; users see canonical structure on day 1.
- [ ] **`test` validate budget.** Should `dashgen recipe test` honor the same budget as `dashgen generate` (default 200), or use a higher per-recipe budget? Proposed: same default; user can override with `--budget`.
- [ ] **`diff` semantic vs textual.** Should `--output unified` show the YAML diff or the rendered query diff? Default: query diff (more useful for "what does my change do?"); add `--output yaml-diff` for the structural view.
- [ ] **`scaffold` template authoring.** Maintainers' YAML scaffolds: who owns updating them when the schema evolves? Proposed: same owner as schema.cue; one-PR update.
- [ ] **`lint --secret-scan` patterns.** Which patterns are flagged? Proposed: leverage existing secret-scanner libraries (e.g. zricethezav/gitleaks core patterns); revisit before Phase E.
- [ ] **Should `recipe list` honor `--no-user-recipes` by default in CI?** No — CLI behavior should be consistent regardless of where it runs.

---

## 13. Acceptance Criteria

The CLI ships when ALL of the following hold:

1. All eight subcommands implemented per their §3 specs.
2. JSON output schemas documented per subcommand (in this doc) and pinned by tests.
3. Performance targets met (§10.3): lint ≤200ms cold, list ≤500ms.
4. All adversary tests (§9.4) pass.
5. Documentation tests (§10.5) pass — no flag drift between docs and code.
6. `dashgen recipe init` + `scaffold` + `lint` + `test` workflow works end-to-end on a clean machine.
7. Pre-commit hook example (§8.1) works on a recipe directory of 50+ files.
8. Existing top-level subcommands (`generate`, `validate`, `inspect`, `lint` (top-level), `coverage`) are unchanged and continue to work.
9. `go test -race -count=1 -timeout 120s ./cmd/dashgen/recipe/...` passes with ≥40 new tests.
10. README.md and `docs/RECIPES-USER-GUIDE.md` (Phase 2 of the DSL plan) cross-reference this spec.

---

## Appendix A: Command Cheatsheet

```bash
# First-time setup
dashgen recipe init

# Author a new recipe from scratch
dashgen recipe scaffold \
    --metric mycorp_queue_depth \
    --type gauge \
    --section saturation \
    --profile service \
    --output ~/.config/dashgen/recipes/mycorp_queue_depth.yaml

# Validate
dashgen recipe lint ~/.config/dashgen/recipes/mycorp_queue_depth.yaml

# Test against a fixture
dashgen recipe test ~/.config/dashgen/recipes/mycorp_queue_depth.yaml \
    --fixture testdata/fixtures/service-realistic

# Inspect
dashgen recipe list --profile service --source user
dashgen recipe show service_http_rate

# Debug "why didn't it fire?"
dashgen recipe explain --name mycorp_queue_depth \
    --metric mycorp_queue_depth \
    --fixture testdata/fixtures/service-realistic

# Compare an override against the built-in
dashgen recipe diff service_http_rate.user.yaml --against-builtin \
    --fixture testdata/fixtures/service-realistic
```

---

## Appendix B: Output Schema (JSON) — `dashgen recipe lint`

```jsonc
[
  {
    "file": "mycorp_queue_depth.yaml",
    "valid": true,
    "elapsed_ms": 42,
    "errors": [],
    "warnings": [
      // Only present in --strict mode or with --secret-scan
      {
        "line": 18,
        "col": 5,
        "code": "non_canonical_unit",
        "message": "unit 'short' is not in the canonical set; consider 'count' or 'ratio'"
      }
    ]
  },
  {
    "file": "broken.yaml",
    "valid": false,
    "elapsed_ms": 51,
    "errors": [
      {
        "line": 5,
        "col": 3,
        "code": "schema_violation",
        "field": "metadata.section",
        "message": "section must be one of [overview traffic errors latency saturation cpu memory disk network pods workloads resources]; got 'satturation'"
      }
    ],
    "warnings": []
  }
]
```

Stable contract: field names will not change in v1.x. New fields may be added; consumers must ignore unknown fields.

---

## Document History

| Date | Author | Change |
|---|---|---|
| 2026-04-27 | initial draft | Eight-subcommand surface; per-subcommand specs; CT1-CT10 adversary catalog; testing + acceptance criteria. |
