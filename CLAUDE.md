# CLAUDE.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

## Codebase Overview

DashGen is a deterministic Prometheus → Grafana dashboard generator (Go,
`module dashgen`, Go 1.25). The v0.1 core ships end-to-end: discover →
classify → recipe-driven synth → 5-stage validate → 3-file render. v0.2
adds a larger recipe catalog (44 recipes total across service/infra/k8s
profiles) and plumbs an optional AI-enrichment seam (`internal/enrich`)
that is explicitly walled off from PromQL generation and verdicts.

**Stack:** Go + cobra (CLI) + yaml.v3 (config) + prometheus/promql parser (validation).
**Structure:** `cmd/dashgen` (CLI) → `internal/app/*` (orchestration) → `internal/{discover,classify,synth,validate,safety,render,recipes,ids,profiles,inventory,ir,config,prometheus,enrich}` (core).
**Tests:** 417+ passing, including per-recipe Match/BuildPanels tables, per-fixture golden + determinism + discrimination regression guards.

### Documentation Index

All long-form docs live under `docs/` (root holds only `README.md`, `CONTRIBUTING.md`, `CLAUDE.md`, `LICENSE`).

| Doc | Purpose |
|---|---|
| [`docs/CODEBASE_MAP.md`](docs/CODEBASE_MAP.md) | **Start here.** Architecture, recipe catalog, fixture layout, navigation cheatsheet. |
| [`docs/PRODUCT_DOC.md`](docs/PRODUCT_DOC.md) | Product scope + release gates (owns what ships in each stage). |
| [`docs/SPECS.md`](docs/SPECS.md) | v0.1 execution contract — non-negotiables, validation pipeline, Rule 5. |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design + package responsibilities. |
| [`docs/STRUCTURE.md`](docs/STRUCTURE.md) | Repo layout + dependency direction. |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Staged timeline + cross-stage rules. |
| [`docs/RECIPES.md`](docs/RECIPES.md) | Recipe catalog + authoring contract + test matrix (current Go-recipe contract). |
| [`docs/RECIPES-DSL.md`](docs/RECIPES-DSL.md) | **v0.3 DRAFT.** YAML wire + CUE schema + text/template runtime spec; full migration plan for all 44 → 47 recipes. Supersedes the Go-recipe contract once Phase 0 of the DSL plan ships. |
| [`docs/RECIPES-DSL-ADVERSARY.md`](docs/RECIPES-DSL-ADVERSARY.md) | **v0.3 DRAFT.** DSL threat model: 20 threats, 15 invariants, adversary corpus, reviewer checklist. |
| [`docs/RECIPES-CLI.md`](docs/RECIPES-CLI.md) | **v0.3 DRAFT.** `dashgen recipe ...` 8-subcommand spec (init / scaffold / lint / list / show / test / explain / diff) + 10-threat CLI adversary catalog. |
| [`docs/V0.2-PLAN.md`](docs/V0.2-PLAN.md) | v0.2 enrichment contract + AI boundary + phased delivery. |
| [`docs/V0.2-REMAINDER.md`](docs/V0.2-REMAINDER.md) | v0.2 RALPLAN-DR consensus implementation plan (historical; everything in scope shipped at v0.2.0). |
| [`docs/V0.3-PLAN.md`](docs/V0.3-PLAN.md) | **v0.3 implementation plan** — 8-phase rollout for the recipe DSL migration with team assignments, per-task DoD, risk register, release acceptance criteria. Companion to the three RECIPES-DSL specs above. |
| [`docs/lint.md`](docs/lint.md) | `dashgen lint` check catalog + JSON output schema (Phase 6). |
| [`docs/coverage.md`](docs/coverage.md) | `dashgen coverage` report schema + family-grouping behavior (Phase 6). |
| [`docs/ADVERSARY.md`](docs/ADVERSARY.md) | Trust validation + code review checklist. |
| [`docs/PRD.md`](docs/PRD.md) | Historical PRD; superseded by `PRODUCT_DOC.md`. |
| [`docs/AI-PROVIDERS.md`](docs/AI-PROVIDERS.md) | AI provider setup, redaction contract, extension contract. |
| [`docs/BIG_ROCKS.md`](docs/BIG_ROCKS.md) | Strategic-revisit doc on recipe authoring & user extensibility (revisit when forcing functions in §9 fire). |

All v0.2 and v0.3 design + implementation plans live under `docs/` as tracked content (see the index above).

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.
