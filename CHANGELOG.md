# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.3.0] — 2026-04-29

### Added

- **YAML+CUE+text/template recipe DSL** replacing the Go-per-recipe authoring contract.
  Schema validated at load time via CUE; PromQL produced by `text/template` with a
  closed helper namespace. See [`docs/RECIPES-DSL.md`](docs/RECIPES-DSL.md).
- **`dashgen recipe` subcommand group** — 8 sub-verbs covering the full authoring lifecycle:
  `init`, `scaffold`, `lint`, `list`, `show`, `test`, `explain`, `diff`.
  See [`docs/RECIPES-CLI.md`](docs/RECIPES-CLI.md).
- **`--recipes-dir` flag** (repeatable) on `dashgen generate` — load additional recipe
  directories alongside the built-ins.
- **XDG default user recipe directory** — `$XDG_CONFIG_HOME/dashgen/recipes/`
  (fallback `~/.config/dashgen/recipes/`) is checked automatically on every run;
  no flag required for the common case.
- **User extensibility** — drop a `*.yaml` recipe in the user dir and it fires alongside
  built-ins. Same-named user recipe shadows the built-in with a deterministic load-time
  warning.
- **30 adversary tests** — 20 DSL adversary tests (threat catalog in
  [`docs/RECIPES-DSL-ADVERSARY.md`](docs/RECIPES-DSL-ADVERSARY.md)) + 10 CLI adversary
  tests (threat catalog in [`docs/RECIPES-CLI.md`](docs/RECIPES-CLI.md) §9.4).
- **Performance benchmarks with budget assertions** — lint ≤200 ms, list ≤500 ms,
  loader ≤500 ms; enforced in `internal/recipes/bench_test.go`.

### Changed

- **44 Go recipes migrated to 47 YAML recipes** — 3 deliberate Tier-C splits:
  `service_db_pool` → `service_db_pool_go_sql_stats` + `service_db_pool_pgxpool`;
  `infra_network` → `infra_network_receive` + `infra_network_transmit`;
  `k8s_container_resources` → `k8s_container_cpu` + `k8s_container_memory`.
- **Goldens regenerated** for `service-realistic`, `infra-basic`, `infra-realistic`,
  `k8s-basic`, `k8s-realistic` — panel UIDs shift because recipe `Name()` changes
  in the three splits; per-panel PromQL content is unchanged.
- **`TestEval_RedosImmunity` wall-clock ceiling** widened from 10 ms to 25 ms to
  accommodate `-race` overhead without flaking (T7.3).

### Removed

- **Per-recipe Go test files** — the YAML harness covers the same surface via
  parameterized `testdata/*.json` fixture tables. Zero `<name>_test.go` files remain
  in `internal/recipes/` for individual recipe logic.
- **44 `<recipe_name>.go` files** from `internal/recipes/` — `service_db_pool.go`,
  `infra_network.go`, `k8s_container_resources.go`, and 41 others. The package now
  contains only: `loader.go`, `matcher.go`, `pair.go`, `registry.go`, `template.go`,
  `helpers.go`, `types.go`, `schema_embed.go`, `data_embed.go`, `yaml_recipe.go`.

## [Unreleased]

### Changed

- T6A.2: Tier-C split — `service_db_pool`, `infra_network`, `k8s_container_resources` split into 6 child recipes; panel UIDs regenerated for affected metrics (v0.3 unreleased, deliberate per V0.3-PLAN).

## [0.2.0] — 2026-04-27

### Added

- **Recipe catalog expanded** from 12 to 44 recipes (service +12, infra +12, k8s +8).
  Every new recipe ships with Match test, BuildPanels test, fixture entries, and
  a discrimination negative-case guard.
- **`dashgen lint`** — offline audit of an existing dashboard bundle against seven
  check classes. See [`docs/lint.md`](docs/lint.md) for the check catalog and JSON
  output schema.
- **`dashgen coverage`** — offline report of metrics covered vs uncovered by a
  dashboard bundle, with family-grouping. See [`docs/coverage.md`](docs/coverage.md)
  for the report schema.
- **`dashgen generate --in-place`** — skip rewriting output files whose content is
  unchanged (idempotent re-runs preserve mtime).
- **Anthropic enrichment provider** (`--provider anthropic`, Phase 3) — opt-in AI
  titles and rationale via `claude-opus-4-7`. Requires `ANTHROPIC_API_KEY`.
- **OpenAI enrichment provider** (`--provider openai`, Phase 4) — same contract over
  `gpt-5`. Requires `OPENAI_API_KEY`. One-file addition validating the registry
  extension contract.
- **Shared enrichment cache** — results keyed by `(InventoryHash, Function,
  ProviderID, PromptHash, DashgenVersion)`; second run over the same inventory
  issues zero outbound requests. Invalidated automatically on prompt or binary
  version change.
- **Redaction guard** (`ValidateBriefs`) — called before every outbound enrichment
  request; enforces that label values, PromQL expressions, and endpoint URLs never
  cross the provider boundary. Pinned by per-provider proxy-capture regression tests.
- **New CLI flags:** `--provider`, `--provider-model`, `--enrich`, `--cache-dir`,
  `--no-enrich-cache`. See [`docs/AI-PROVIDERS.md`](docs/AI-PROVIDERS.md).
- **New IR fields** `Panel.MechanicalTitle` and `Panel.RationaleExtra` — populated
  only when enrichment runs; absent (zero-value) in `--provider off` output so
  existing tooling is unaffected.
- **`--log-enrichment-payloads`** debug flag (hidden unless `DASHGEN_DEBUG=1`) —
  emits one line per outbound enrichment call to stderr for local diagnostics.

### Changed

- **Panel-ID modulus** widened from `2^31-1` (int32 max) to `9007199254740881`
  (largest prime below `2^53`) to eliminate cross-panel UID collisions while
  staying within `Number.MAX_SAFE_INTEGER` for Grafana JS consumers (commit
  `6d3c8e0`).
- **Help-text trait hints** in `internal/classify` are now gated by an infra-label
  allowlist, preventing false-positive trait assignments from ambiguous help strings.

### Fixed

- Panel-ID and panel-UID cross-collisions in golden fixtures exposed by the widened
  modulus change.
