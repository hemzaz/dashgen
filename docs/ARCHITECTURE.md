# DashGen Architecture

## Document Status
- Product: DashGen
- Language: Go
- Status: implementation architecture draft
- Purpose: define the technical architecture for a deterministic, CLI-first dashboard synthesis tool for cloud engineers

## 1. Why Go
DashGen will be written in Go.

This is a pragmatic choice:
- cloud engineers already use Go-heavy tooling across Kubernetes, Prometheus, Terraform ecosystems, and cloud-native CLIs
- Go produces portable static binaries that are easy to ship in CI, containers, and internal tooling
- concurrency, structured error handling, and strong standard-library support fit networked CLI workloads well
- the language encourages clear package boundaries and predictable operational behavior

DashGen is a CLI and validation engine, not a dynamic application server. Go fits that shape directly.

## 2. Architecture Goals
The architecture must preserve the product rules from `PRODUCT_DOC.md`:
- deterministic first
- safe by default
- reviewable output
- bounded backend load
- stable reruns and Git-friendly diffs
- narrow v0.1 scope

From an implementation standpoint, that means:
- the core pipeline must be pure and testable where possible
- backend access must be isolated behind interfaces
- Grafana rendering must not leak into synthesis logic
- validation must be explicit and staged
- every decision that affects output stability must be deterministic by default

## 3. System Overview
DashGen is a batch-style CLI pipeline:

1. Load config and CLI inputs
2. Discover metrics from one Prometheus-compatible backend
3. Build a normalized metric inventory
4. Classify metrics deterministically
5. Match known recipes and synthesize dashboard candidates into an IR
6. Validate candidate queries and assign safety verdicts
7. Finalize dashboard IR
8. Render outputs:
   - Grafana JSON
   - rationale Markdown
   - machine-readable warnings summary

The system is intentionally one-way. v0.1 does not mutate Grafana or write back to Prometheus.

## 4. High-Level Components
### CLI layer
Responsible for:
- command parsing
- config loading
- environment and flag merging
- output paths
- exit codes
- user-facing error formatting

This layer should stay thin. It should orchestrate application services, not contain synthesis logic.

### Application layer
Responsible for:
- command workflows such as `generate`, `validate`, and `inspect`
- lifecycle of a single run
- context propagation
- assembling dependencies
- coordinating discovery, synthesis, validation, and rendering

This layer owns the use cases.

### Domain layer
Responsible for:
- metric inventory model
- classification rules
- recipe matching
- dashboard IR
- safety model
- validation verdicts
- deterministic ordering and ID policies

This layer contains the core product logic and should remain independent of transport and storage details.

### Infrastructure layer
Responsible for:
- Prometheus HTTP API client
- PromQL parser integration
- Grafana JSON rendering
- filesystem IO
- config file parsing
- logging

This layer adapts external systems and libraries to the domain interfaces.

## 5. Proposed Repository Layout
```text
dashgen/
  cmd/
    dashgen/
      main.go
  internal/
    app/
      generate/
      validate/
      inspect/
    config/
    discover/
    classify/
    recipes/
    synth/
    validate/
    safety/
    inventory/
    ir/
    render/
      grafana/
      rationale/
      warnings/
    prometheus/
    ids/
    profiles/
    output/
    fixtures/
    testutil/
  pkg/
    api/
  docs/
  testdata/
    fixtures/
    goldens/
```

### Layout notes
- `cmd/dashgen` contains only binary entrypoint wiring
- `internal/` holds the implementation and protects unstable internals from becoming public API accidentally
- `pkg/api` is optional and should stay tiny; use it only if DashGen later exposes stable reusable types
- `testdata/fixtures` and `testdata/goldens` are first-class product artifacts

## 6. Core Data Flow
### Step 1: Input resolution
Inputs come from:
- CLI flags
- config file
- environment if needed later

The application resolves these into one immutable `RunConfig`.

### Step 2: Discovery
The discovery subsystem queries one Prometheus-compatible HTTP query API endpoint for:
- metric names
- metadata such as type and help text where available
- label hints and sample observations

Output: raw backend discovery data.

### Step 3: Inventory normalization
The inventory builder transforms raw discovery data into a normalized `MetricInventory`.

Responsibilities:
- canonicalize label ordering
- normalize missing or partial metadata
- attach inferred unit and family
- preserve enough provenance for inspectability

Output: stable inventory model.

### Step 4: Classification
The classifier runs deterministic rules over the inventory.

Responsibilities:
- detect counters, gauges, and classic histogram families
- infer common suffix patterns
- identify service, infra, and k8s hints
- assign recipe-relevant traits

Output: classified inventory with structured hints.

### Step 5: Recipe matching and synthesis
The recipe engine uses the classified inventory plus selected profile to produce dashboard candidates.

Responsibilities:
- choose sections relevant to the profile
- construct candidate panels and query templates
- annotate confidence and rationale before validation
- omit weak sections instead of fabricating filler

Output: pre-validation dashboard IR.

### Step 6: Validation and safety
Each candidate query passes the staged validation pipeline.

Responsibilities:
- parse PromQL
- run selector and label sanity checks
- execute bounded backend validation
- run safety and cardinality scoring
- return `accept`, `accept_with_warning`, or `refuse`

Output: validated dashboard IR with warnings and safety verdicts attached.

### Step 7: Finalization
The finalizer removes refused panels, stabilizes ordering, and assigns stable IDs.

Responsibilities:
- enforce panel caps
- drop empty sections
- preserve deterministic ordering
- ensure stable dashboard and panel identity

Output: final dashboard IR.

### Step 8: Rendering
Separate renderers emit:
- Grafana dashboard JSON
- rationale Markdown
- warnings summary JSON or YAML

Renderers consume the IR only. They should not know how discovery or validation works.

## 7. Key Domain Models
These are conceptual models first. Final Go types may differ slightly, but the boundaries should remain.

### RunConfig
Contains:
- Prometheus URL
- profile
- output options
- validation mode
- strict mode
- scoped selectors such as job, namespace, metric regex
- config overrides

### MetricInventory
Contains:
- metrics keyed by canonical metric name
- normalized metadata
- label sets and sample label observations
- inferred units
- inferred families
- classification annotations

### MetricDescriptor
Represents one metric family or metric identity, including:
- name
- type
- help text
- labels
- source metadata confidence
- inferred traits

### DashboardIR
Contains:
- dashboard identity
- profile
- rows or groups
- panels
- variables
- dashboard-level warnings

### PanelIR
Contains:
- stable key
- title
- visualization kind
- query candidates
- confidence
- warnings
- safety verdict
- rationale fragments

### QueryCandidate
Contains:
- expression
- legend or label strategy
- unit
- validation results
- cardinality risk
- refusal reason if omitted

### ValidationResult
Contains:
- parse result
- execution result summary
- safety verdict
- warning codes
- refusal codes

## 8. Go Package Boundaries
### `internal/config`
Loads and validates user configuration.

Should own:
- config structs
- file parsing
- defaults
- merge logic

Should not own:
- business logic beyond config validation

### `internal/prometheus`
Implements backend access.

Should own:
- HTTP client
- API request and response handling
- retry policy for discovery-only operations where appropriate
- timeout handling

Should expose narrow interfaces to the rest of the system.

### `internal/discover`
Orchestrates backend reads into raw discovery results.

Should own:
- which API calls are required for inventory building
- bounded collection logic
- discovery-specific error wrapping

### `internal/inventory`
Builds normalized inventory models.

Should own:
- canonical ordering
- metadata normalization
- inferred family and unit attachment

### `internal/classify`
Runs deterministic classification rules.

Should own:
- suffix and type heuristics
- histogram family detection
- service and infra hint derivation

### `internal/recipes`
Defines known patterns for supported dashboards.

Should own:
- recipe registry
- profile-to-recipe mapping
- query template definitions
- recipe match scoring

### `internal/synth`
Transforms classified inventory into dashboard IR candidates.

Should own:
- section construction
- panel assembly
- candidate generation

### `internal/validate`
Runs the validation pipeline.

Should own:
- PromQL parse stage integration
- execution checks
- warning and refusal result construction

### `internal/safety`
Evaluates risky labels and cardinality.

Should own:
- denylist logic
- risk scoring
- thresholds
- policy decisions such as downgrade versus refuse

### `internal/ir`
Defines the dashboard intermediate representation.

This package should remain stable and intentionally boring.

### `internal/ids`
Generates deterministic stable IDs and keys.

Should own:
- hashing inputs
- canonical key material
- dashboard ID generation
- panel ID generation

### `internal/render/grafana`
Renders IR to Grafana dashboard JSON.

Should own:
- Grafana schema mapping
- datasource variable emission
- deterministic JSON field ordering where practical

### `internal/render/rationale`
Renders Markdown explaining:
- what was included
- what was refused
- warnings and confidence
- profile and config context

### `internal/render/warnings`
Renders machine-readable warnings summary.

### `internal/app/...`
Implements command-level workflows.

Example:
- `app/generate` calls discovery, inventory, classification, synthesis, validation, finalization, and rendering
- `app/validate` validates candidates or outputs without necessarily writing all artifacts
- `app/inspect` surfaces internal reasoning for review

## 9. Determinism Strategy
Determinism is a product requirement, not an implementation detail.

DashGen must make deterministic choices in:
- map iteration
- label ordering
- recipe selection ties
- panel ordering
- section ordering
- ID generation
- warning emission order

Implementation rules:
- never rely on Go map iteration order
- sort all externally visible collections before output
- use canonical string material for stable IDs
- make tie-breakers explicit in code
- keep time-dependent fields out of outputs unless explicitly documented

## 10. Validation Architecture
### Stages
The validation subsystem should be implemented as explicit stages:

1. Parse stage
2. Selector sanity stage
3. Execution stage
4. Safety stage
5. Verdict stage

This can be implemented as a small pipeline over `QueryCandidate`.

### Execution policy
Default v0.1 policy:
- instant queries by default
- bounded range sampling only where needed
- short per-query timeout
- cap on total validation work per run

### Failure policy
- parse failure: refuse
- selector failure: refuse
- safety failure: refuse
- backend execution failure: refuse by default
- transient backend failures may become warnings only in non-strict exploratory modes if the product later supports that nuance cleanly

### Why stage boundaries matter
They make it possible to:
- test each failure mode directly
- explain results through `inspect`
- evolve heuristics without rewriting the whole validator

## 11. Safety Architecture
Safety decisions should not be scattered through recipes or renderers.

They belong in a dedicated policy layer that evaluates:
- banned labels
- suspicious grouping dimensions
- estimated series explosion
- profile-specific panel appropriateness

This keeps risk handling auditable and testable.

## 12. Rendering Architecture
Renderers must be dumb translators from IR to output formats.

### Grafana renderer responsibilities
- emit one dashboard JSON document
- define `$datasource`
- map panel IR to Grafana panel schema
- preserve stable field ordering where possible

### Rationale renderer responsibilities
- explain selected profile
- summarize included and omitted sections
- show warning and refusal reasons
- make reviewer feedback easier

### Warnings renderer responsibilities
- emit machine-readable diagnostics for CI or later tooling

## 13. Error Handling Strategy
Errors should be categorized early.

Suggested categories:
- configuration error
- backend connectivity error
- backend protocol or compatibility error
- discovery error
- validation refusal
- rendering error
- filesystem error
- internal bug

Principles:
- user-facing errors should be actionable
- refusal is not always a fatal error; it can be part of normal safe behavior
- strict-mode violations should return non-zero cleanly without stack-noise
- internal bugs should include enough structured context for debugging

## 14. Logging and Diagnostics
v0.1 does not need a complex logging framework, but it does need predictable diagnostics.

Suggested approach:
- human-readable stderr by default
- structured debug logs behind a flag later if needed
- rationale and inspect output as first-class diagnostics, not just logs

Important: logging must not become part of the stable contract for tests unless intentionally captured.

## 15. Concurrency Model
Go gives easy concurrency, but DashGen should use it conservatively.

Use concurrency for:
- bounded backend reads
- validation tasks with explicit worker limits

Do not use concurrency where it makes output order or reasoning hard to reproduce.

Rules:
- concurrency limits must be explicit
- result ordering must be normalized after concurrent work
- no unbounded goroutine fan-out

## 16. Testing Strategy
### Unit tests
Focus on:
- classification rules
- recipe matching
- safety scoring
- ID stability
- renderer determinism

### Fixture tests
Use real or representative metric inventories to test:
- discovery normalization
- end-to-end synthesis
- validation outcomes
- output stability

### Golden tests
Maintain golden files for:
- dashboard JSON
- rationale Markdown
- warnings summary

### Contract tests
For Prometheus integration, add narrow tests around:
- supported API calls
- timeout handling
- compatibility assumptions

## 17. Suggested External Dependencies
Keep dependencies minimal.

Reasonable categories:
- CLI framework
- YAML or TOML config parser
- Prometheus model or parser libraries where stable and appropriate
- test helpers

Selection rule:
- prefer mature, boring libraries over clever abstractions
- avoid dependency-heavy frameworks
- keep the domain model independent of external package types

## 18. Security and Operational Posture
DashGen is low-risk compared to controllers or agents, but it still needs sane defaults.

Requirements:
- no credential exfiltration through logs
- no network calls except configured backend access
- no hidden write-back behavior
- bounded query budgets
- safe refusal when uncertain

## 19. Future Extension Points
The architecture should leave room for v0.2 without polluting v0.1.

Likely extension points:
- alternate discovery sources such as `/metrics`
- optional enrichment providers
- lint and coverage commands
- partial regeneration

To support that safely:
- keep discovery behind interfaces
- keep enrichment outside the deterministic core
- keep the IR stable and renderer-separated

## 20. Recommended First Implementation Slice
Build in this order:

1. CLI scaffold and config loading
2. Prometheus client and discovery
3. inventory and classification packages
4. dashboard IR and stable ID package
5. recipe engine for one profile, ideally `service`
6. validation pipeline with parse plus bounded execution
7. Grafana JSON renderer
8. rationale renderer
9. fixture corpus and golden tests
10. `inspect` command

This sequence gets to a believable deterministic core quickly and matches the product roadmap.

## 21. Non-Goals for the Architecture
This architecture should not optimize for:
- plugin systems in v0.1
- distributed execution
- persistent daemons
- live reconciliation loops
- generic observability platform abstractions

If a design choice helps a hypothetical future platform but makes v0.1 less clear or less deterministic, reject it.
