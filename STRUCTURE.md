# DashGen Repository Structure

## Purpose
This document proposes a repository structure for DashGen that is:
- modular
- maintainable
- easy to scaffold in Go
- suitable for a CLI-first v0.1
- extensible without turning the codebase into speculative architecture

The goal is not to design for every future feature today.
The goal is to make likely future changes cheap without making current implementation blurry.

This document should be read together with:
- [PRODUCT_DOC.md](/Users/elad/PROJ/dashgen/PRODUCT_DOC.md)
- [ARCHITECTURE.md](/Users/elad/PROJ/dashgen/ARCHITECTURE.md)
- [SPECS.md](/Users/elad/PROJ/dashgen/SPECS.md)
- [ADVERSARY.md](/Users/elad/PROJ/dashgen/ADVERSARY.md)

## 1. Design Goals
The repository should optimize for:
- a thin CLI and strong internal domain core
- deterministic, testable behavior
- clear ownership by package
- low-friction feature delivery
- safe extension points for future commands and sources
- stable fixture and golden-test workflows

It should not optimize for:
- plugin marketplaces in v0.1
- general-purpose framework patterns
- complicated dependency injection systems
- multiple backends before the first backend is solid
- abstract interfaces added only because they might be useful someday

## 2. Top-Level Repository Layout
Recommended layout:

```text
dashgen/
  cmd/
    dashgen/
      main.go

  internal/
    app/
    classify/
    config/
    discover/
    ids/
    inventory/
    ir/
    profiles/
    prometheus/
    recipes/
    render/
    safety/
    synth/
    validate/

  pkg/

  testdata/
    fixtures/
    goldens/
    samples/

  docs/

  scripts/

  .github/

  go.mod
  go.sum
  README.md
  PRODUCT_DOC.md
  ARCHITECTURE.md
  SPECS.md
  ADVERSARY.md
  STRUCTURE.md
```

## 3. Why This Layout
### `cmd/`
Holds executable entrypoints only.

Why:
- keeps startup wiring separate from product logic
- makes it easier to add future binaries without moving domain code

Current need:
- one binary: `dashgen`

Possible future additions:
- `dashgen-dev`
- `dashgen-fixture`

Rule:
- if business logic shows up in `cmd/`, the structure is already slipping

### `internal/`
Holds all unstable implementation details.

Why:
- DashGen is still defining its product shape
- internal packages prevent accidental public API commitments
- package boundaries remain useful without exposing them as SDK promises

Rule:
- default new code goes into `internal/`
- do not create `pkg/` packages casually

### `pkg/`
Reserved for intentionally public, reusable APIs.

Why:
- many Go repos expose too much too early
- public packages become maintenance burdens

Recommendation:
- keep `pkg/` empty in the early implementation
- only add to it if DashGen truly needs a stable external library surface

### `testdata/`
Holds fixtures, golden outputs, and sample inputs.

Why:
- fixtures are product assets, not just test leftovers
- output stability is central to trust
- contributors need one canonical place for reproducible examples

### `docs/`
Holds user-facing and contributor-facing documents that are not root-level governing docs.

Examples:
- usage examples
- fixture format docs
- recipe authoring notes
- contributor guides

### `scripts/`
Holds repo-maintenance scripts.

Examples:
- golden refresh helpers
- fixture validation scripts
- CI convenience tasks

Rule:
- scripts should support the product, not replace core product logic

## 4. Recommended Internal Package Structure
Suggested layout:

```text
internal/
  app/
    generate/
    validate/
    inspect/

  config/
  discover/
  prometheus/
  inventory/
  classify/
  profiles/
  recipes/
  synth/
  safety/
  validate/
  ids/
  ir/

  render/
    grafana/
    rationale/
    warnings/

  testutil/
```

This structure is intentionally product-shaped rather than layer-purist.

## 5. Package Responsibilities
### `internal/app/`
Owns command workflows.

Responsibilities:
- orchestration of end-to-end use cases
- dependency assembly for a single command
- command-level result handling

Subpackages:
- `app/generate`
- `app/validate`
- `app/inspect`

Why separate these:
- each command has different output and failure semantics
- keeps command orchestration from bloating one generic service package

Future-friendly:
- adding `lint`, `coverage`, or `enrich` later becomes additive, not invasive

### `internal/config/`
Owns config parsing, defaults, merge rules, and validation.

Responsibilities:
- config file parsing
- merging file config with CLI flags
- resolved runtime config model

Future-friendly:
- if later adding profile-specific config, feature flags, or provider config, it belongs here

Avoid:
- letting config structs mirror every internal implementation struct directly

### `internal/prometheus/`
Owns the concrete Prometheus-compatible HTTP client.

Responsibilities:
- API calling
- timeout handling
- request shaping
- backend response normalization at the transport level

Future-friendly:
- if later supporting another source or a mock backend, this package remains one implementation behind a small interface

Avoid:
- leaking HTTP response types into domain packages

### `internal/discover/`
Owns discovery workflows, not transport.

Responsibilities:
- which backend calls are needed
- collecting the data required for metric inventory
- bounding discovery cost

Why separate from `prometheus/`:
- transport code and discovery strategy change for different reasons

Future-friendly:
- if later adding `/metrics` input, a second discovery backend can plug in here without rewriting inventory logic

### `internal/inventory/`
Owns the normalized metric inventory model and builders.

Responsibilities:
- canonical inventory creation
- inferred units and family grouping
- provenance preservation
- label normalization

Why this matters:
- inventory is the base contract for classification and synthesis

Future-friendly:
- changes in discovery source should not force downstream logic to care

### `internal/classify/`
Owns deterministic metric classification.

Responsibilities:
- counters, gauges, histograms
- suffix heuristics
- service/infra/k8s hints

Future-friendly:
- adding more heuristics should not require touching renderers or command packages

Avoid:
- mixing safety rules here
- mixing recipe definitions here

### `internal/profiles/`
Owns profile definitions and profile-level defaults.

Responsibilities:
- profile identity and allowed values
- panel caps
- section ordering defaults
- profile-specific constraints

Why have this package:
- avoids burying profile logic across recipes and synthesis
- gives one place to answer "what does `service` mean?"

Future-friendly:
- adding a future `database` or `queue` profile becomes clearer

### `internal/recipes/`
Owns supported known recipes and recipe registry.

Responsibilities:
- recipe definitions
- recipe matching conditions
- panel/query template material

Future-friendly:
- this is the right place to add new metric families later

Avoid:
- stuffing safety exceptions directly into recipes
- making recipes depend on Grafana rendering details

### `internal/synth/`
Owns dashboard synthesis into IR.

Responsibilities:
- section assembly
- panel candidate assembly
- confidence pre-annotation
- omission of weak sections before rendering

Future-friendly:
- if later adding partial regeneration, this package is one of the change centers

### `internal/safety/`
Owns risk policy.

Responsibilities:
- banned labels
- risky grouping logic
- cardinality scoring
- downgrade vs refuse policy

Why isolate it:
- safety is product-critical and must be auditable

Future-friendly:
- if later allowing opt-in risky modes, the complexity lands here instead of diffusing everywhere

### `internal/validate/`
Owns query validation.

Responsibilities:
- syntax validation
- selector sanity checks
- bounded execution validation
- verdict construction

Why separate from `safety/`:
- safety and technical validity overlap, but are not the same thing

Future-friendly:
- if later adding lint or coverage logic, parts of this package may be reused without mixing concerns

### `internal/ids/`
Owns stable key and ID generation.

Responsibilities:
- canonical key material
- dashboard ID generation
- panel ID generation

Why isolate it:
- determinism failures often hide in small identity changes

Future-friendly:
- if later adding partial regeneration or diff-aware updates, this package becomes even more critical

### `internal/ir/`
Owns the dashboard intermediate representation.

Responsibilities:
- IR structs
- enums or typed constants
- IR validation helpers if needed

Why isolate it:
- IR is the seam between synthesis and rendering

Future-friendly:
- enables adding new renderers later without contaminating core synthesis logic

### `internal/render/`
Owns output translation.

Subpackages:
- `render/grafana`
- `render/rationale`
- `render/warnings`

Why split renderers:
- each output has different stability rules
- rationale and warnings evolve independently from Grafana schema mapping

Future-friendly:
- later output formats can be added without changing synthesis

### `internal/testutil/`
Owns shared helpers for tests.

Responsibilities:
- fixture loading
- golden comparison helpers
- test builders for inventory and IR

Rule:
- test helpers must simplify tests, not hide important assertions

## 6. Proposed Scaffolding Strategy
Do not scaffold the entire repo at once.
Scaffold in value order.

### Phase 1: minimal viable skeleton
Create:

```text
cmd/dashgen/main.go
internal/app/generate/
internal/config/
internal/prometheus/
internal/discover/
internal/inventory/
internal/classify/
internal/recipes/
internal/synth/
internal/validate/
internal/safety/
internal/ir/
internal/ids/
internal/render/grafana/
internal/render/rationale/
testdata/fixtures/
testdata/goldens/
```

This is enough to build the first real vertical slice.

### Phase 2: command expansion
Add:

```text
internal/app/validate/
internal/app/inspect/
internal/render/warnings/
internal/testutil/
```

### Phase 3: maintenance and contributor ergonomics
Add:

```text
docs/
scripts/
.github/
```

Examples:
- `docs/fixtures.md`
- `docs/recipes.md`
- `scripts/update-goldens.sh`
- `.github/workflows/test.yml`

## 7. Future Change Scenarios
This section answers "what if I need feature XXX later?"

### Scenario: add a new profile
Example:
- `database`
- `queue`
- `cache`

Expected repo impact:
- `internal/profiles/`
- `internal/recipes/`
- `internal/synth/`
- `testdata/fixtures/`
- `testdata/goldens/`

What should not need major changes:
- `internal/prometheus/`
- `internal/render/grafana/`
- `cmd/dashgen/`

If renderer or transport changes heavily for a new profile, the structure is leaking.

### Scenario: add `/metrics` input later
Expected repo impact:
- new source implementation near `internal/discover/`
- possibly a new transport/parser package
- discovery wiring in `internal/app/`

What should not need major changes:
- `internal/inventory/`
- `internal/classify/`
- `internal/recipes/`
- `internal/ir/`
- `internal/render/`

If these packages need source-specific hacks, the domain boundary is too weak.

### Scenario: add AI enrichment later
Expected repo impact:
- new package group, likely `internal/enrich/`
- optional orchestration changes in `internal/app/`
- possible metadata augmentation before or after classification, depending on design

What should not change fundamentally:
- deterministic path still exists
- `internal/safety/` still owns safety
- `internal/validate/` still validates all emitted queries
- `internal/render/` remains unchanged except for extra rationale content

If AI touches renderers directly or bypasses validation, the architecture is broken.

### Scenario: add `lint`
Expected repo impact:
- `internal/app/lint/`
- likely reusable parts from `internal/validate/` and `internal/safety/`
- maybe a dedicated `internal/lint/` package if logic grows

What should not need major changes:
- discovery and synthesis path for `generate`

### Scenario: add `coverage`
Expected repo impact:
- `internal/app/coverage/`
- possible `internal/coverage/`
- reuse of inventory, recipe, and IR knowledge

What should not change:
- renderers and ID logic

### Scenario: add partial regeneration
Expected repo impact:
- `internal/synth/`
- `internal/ids/`
- perhaps a dedicated `internal/regenerate/`

What must remain stable:
- ID semantics
- IR contract
- refusal and safety rules

If partial regeneration requires rewriting the whole render pipeline, the seams are wrong.

### Scenario: support another renderer later
Example:
- another dashboard target
- richer machine-readable export

Expected repo impact:
- add new package under `internal/render/`

What should not change:
- `internal/recipes/`
- `internal/synth/`
- `internal/classify/`

If the core domain must become renderer-specific, the IR is not doing its job.

## 8. Dependency Direction
Keep dependencies pointing inward toward the domain, not sideways in random directions.

Preferred flow:

```text
cmd -> app -> discover/classify/recipes/synth/validate/safety -> ir -> render
                     \-> prometheus
                     \-> config
                     \-> inventory
                     \-> ids
```

Rules:
- renderers depend on IR, not on discovery or transport
- recipes depend on inventory and profile concepts, not on Grafana JSON types
- safety does not depend on renderers
- command packages do not import deeply across each other

Smell:
- if one package imports almost everything, it is probably the wrong abstraction boundary

## 9. File-Level Structure Within Packages
Keep packages small enough to understand, but not artificially fragmented.

Example for `internal/validate/`:

```text
internal/validate/
  service.go
  parse.go
  selector.go
  execute.go
  verdict.go
  types.go
```

Example for `internal/recipes/`:

```text
internal/recipes/
  registry.go
  types.go
  service_http.go
  infra_cpu.go
  infra_memory.go
  k8s_pods.go
```

Rule:
- split files by responsibility, not by tiny helper count

## 10. Naming Guidance
Prefer names that reflect product language:
- `inventory`
- `classify`
- `synth`
- `validate`
- `safety`
- `render`
- `profiles`
- `recipes`

Avoid vague buckets like:
- `core`
- `common`
- `utils`
- `helpers`
- `manager`
- `engine` everywhere

If a package name does not tell you what product concept it owns, rename it.

## 11. Maintainability Rules
To keep the repo healthy over time:

### Rule 1: do not create global utility dumping grounds
Bad:
- `internal/util`
- `internal/common`
- `pkg/shared`

Instead:
- put helpers near the domain they serve

### Rule 2: keep interfaces close to consumers
Do not create interface-only packages.

Instead:
- define a small interface at the package that consumes it
- keep concrete implementations elsewhere

### Rule 3: keep fixtures close to trust boundaries
Add fixtures for:
- new recipes
- new safety rules
- new classification edge cases
- stability-sensitive rendering changes

### Rule 4: make stability-sensitive code obvious
Code that affects:
- ordering
- IDs
- renderer field layout
- refusal logic

should be easy to find and heavily tested.

### Rule 5: prefer additive growth
Adding a feature should usually mean:
- a new package
- a new file
- a new fixture

not rewriting half the tree

## 12. Suggested Docs Structure
Root docs should remain the governing docs:
- `README.md`
- `PRODUCT_DOC.md`
- `ARCHITECTURE.md`
- `SPECS.md`
- `ADVERSARY.md`
- `STRUCTURE.md`

Put secondary docs in `docs/`:

```text
docs/
  fixtures.md
  recipes.md
  config.md
  contributing.md
  examples/
```

Why:
- root stays focused on product and engineering governance
- `docs/` holds the evolving operational detail

## 13. Suggested Test Structure
Use both package-local tests and fixture-driven tests.

Recommended patterns:
- `*_test.go` next to packages for unit logic
- `testdata/fixtures/` for normalized backend input sets
- `testdata/goldens/` for expected outputs

Possible layout:

```text
testdata/
  fixtures/
    service-basic/
    infra-node/
    k8s-small/
  goldens/
    service-basic/
      dashboard.json
      rationale.md
      warnings.json
```

This makes it easy to answer:
- what inputs are we testing?
- what output do we expect?
- what changed in a diff?

## 14. Recommended Initial Scaffold
If starting today, scaffold this:

```text
cmd/dashgen/main.go

internal/app/generate/service.go
internal/config/config.go
internal/prometheus/client.go
internal/discover/discover.go
internal/inventory/types.go
internal/inventory/build.go
internal/classify/classify.go
internal/profiles/profiles.go
internal/recipes/registry.go
internal/synth/synth.go
internal/safety/policy.go
internal/validate/validate.go
internal/ir/types.go
internal/ids/ids.go
internal/render/grafana/render.go
internal/render/rationale/render.go

testdata/fixtures/.keep
testdata/goldens/.keep
```

This is enough to start without overcommitting to a premature tree.

## 15. Final Structural Rule
The repository should make the next obvious feature easy.
It should not try to make every imaginable future feature easy.

When in doubt:
- optimize for the next real slice
- preserve clean seams
- avoid speculative indirection
- keep the deterministic path obvious

If a structural choice mainly helps a hypothetical future and makes today’s code harder to follow, reject it.
