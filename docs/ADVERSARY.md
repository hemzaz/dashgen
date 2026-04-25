# DashGen Adversary Review

## Purpose
This document attacks DashGen from the adversarial side.

Its job is not to be supportive. Its job is to break confidence cheaply before production users do.

It asks:
- where can the product lie?
- where can the implementation drift from the docs?
- where can determinism silently fail?
- where can "safe by default" collapse under real data?
- where are we underspecified and pretending otherwise?

This file should be used alongside:
- [PRODUCT_DOC.md](/Users/elad/PROJ/dashgen/PRODUCT_DOC.md)
- [ARCHITECTURE.md](/Users/elad/PROJ/dashgen/ARCHITECTURE.md)
- [SPECS.md](/Users/elad/PROJ/dashgen/SPECS.md)

## 1. Adversarial Posture
Assume the following:
- backend metadata is incomplete, inconsistent, or wrong
- metric names are misleading
- label sets are noisier and larger than expected
- Prometheus-compatible backends are only mostly compatible
- users will run the tool in CI and blame instability immediately
- generated dashboards will be judged harder than handwritten ones
- contributors will accidentally optimize for demo quality over correctness

If DashGen is robust under those assumptions, it is probably viable.

## 2. Primary Attack Themes
Every design and implementation choice should be attacked along these dimensions:

### Truthfulness
Is the dashboard actually supported by the metrics, or does it merely look plausible?

### Stability
Will the same input really produce the same output across reruns, machines, and Go versions?

### Safety
Can the tool generate expensive, misleading, or high-cardinality queries despite claiming guardrails?

### Scope control
Can the codebase quietly accumulate future-looking abstractions that weaken v0.1 execution?

### Reviewability
If something is omitted, downgraded, or refused, can a reviewer see why?

### Operational realism
Will this still behave acceptably on large, messy, production-like metric inventories?

## 3. Product Claims To Attack
The product makes a few core claims. Each one must be treated as suspect until proven.

### Claim: deterministic first-pass dashboards
Adversarial question:
- what exact evidence proves deterministic behavior?

Ways this claim fails:
- map iteration leaks into output ordering
- stable IDs depend on non-canonical fields
- recipe tie-breakers are implicit
- warnings are emitted in nondeterministic order
- JSON output changes because of renderer implementation details

Required evidence:
- rerun tests on identical fixtures
- golden tests for output ordering
- explicit tie-break rules in code
- stable-ID tests over representative inputs

### Claim: safe by default
Adversarial question:
- safe against what, exactly?

Ways this claim fails:
- high-cardinality labels slip through because denylist is incomplete
- cardinality estimation is too weak to catch result explosions
- range-query sampling becomes expensive under broad selectors
- "transient backend failures" become an excuse to emit weak dashboards

Required evidence:
- tests for banned labels
- tests for downgraded and refused large-result candidates
- hard budgets enforced in code, not just docs
- explicit policy around warning-grade transient failures

### Claim: reviewable outputs
Adversarial question:
- reviewable to whom, and under what time pressure?

Ways this claim fails:
- rationale is too vague to explain omissions
- warnings do not map back to concrete panels or query candidates
- inspect output is too low-level or too noisy
- diff churn makes review impractical

Required evidence:
- fixture examples with rationale that a reviewer can actually follow
- stable output with minimal churn
- refusal reasons that are specific, not generic

## 4. Attack Every Major Decision
This section treats the major product choices as suspicious until proven.

### Decision: one backend endpoint per run
Attack:
- does one endpoint mean one logical cluster, one tenant, one namespace, or literally one URL?
- what happens when the backend is only partially Prometheus-compatible?
- what if metadata APIs work but query semantics differ?

Failure modes:
- false confidence from "compatible enough" systems
- support burden from unofficial backends
- subtle query mismatches that slip through validation

Required countermeasures:
- clearly isolate reference-supported behavior
- test compatibility assumptions narrowly
- do not overclaim support

### Decision: minimal templating in v0.1
Attack:
- are static dashboards too rigid for real operator workflows?
- does `$datasource` alone make the dashboard usable enough?

Failure modes:
- users immediately patch every generated dashboard by hand
- dashboards are technically valid but operationally clumsy

Required countermeasures:
- ensure profiles are useful without variable-heavy dashboards
- document the tradeoff honestly

### Decision: no `/metrics` mode before v0.2
Attack:
- does this remove too many otherwise interested users?
- are we overfitting architecture to Prometheus APIs only?

Failure modes:
- discovery code bakes in assumptions that block later source abstraction
- users in pre-Prometheus environments ignore the product completely

Required countermeasures:
- keep discovery behind interfaces
- do not contaminate core domain types with transport-specific details

### Decision: balanced panel count, roughly 5 to 8
Attack:
- is that enough to be useful?
- or enough to look useful while hiding missing coverage?

Failure modes:
- dashboards feel sparse and underwhelming
- teams infer false completeness from a neat-looking overview

Required countermeasures:
- rationale should make omissions obvious
- coverage gaps should be visible, not hidden

### Decision: narrow histogram support
Attack:
- are we refusing too much value?
- or still pretending to understand histograms we do not?

Failure modes:
- incorrect percentile math
- misleading bucket aggregations
- overconfidence around latency charts

Required countermeasures:
- whitelist only well-understood histogram recipes
- refuse ambiguous cases loudly

## 5. Architecture Attack Surface
The architecture should be treated as a potential source of future failure, not just order.

### Thin CLI, fat domain
Attack:
- does the CLI stay thin in practice?
- or does command handling accumulate business logic because it is convenient?

Watch for:
- command-specific branching that bypasses common validation
- duplicated orchestration between `generate`, `validate`, and `inspect`

### Renderer-separated IR
Attack:
- is the IR actually generic, or just Grafana JSON in disguise?

Watch for:
- Grafana-specific field names leaking into domain types
- renderer constraints shaping synthesis prematurely
- IR fields that exist only to satisfy current JSON output

### Dedicated safety layer
Attack:
- is safety centralized, or does it leak into recipes and ad hoc conditionals?

Watch for:
- denylist checks inside random packages
- recipe-specific safety exceptions
- render-time omissions that bypass formal verdicts

### Bounded concurrency
Attack:
- are worker pools truly bounded?
- is result ordering normalized after concurrent work?

Watch for:
- hidden goroutine fan-out
- flaky tests caused by racing output assembly
- nondeterministic warnings or IDs

## 6. Spec Drift Attacks
The most likely failure is not one big bug. It is gradual spec drift.

### Drift pattern: broad scaffolding before real behavior
Symptoms:
- many packages, little working behavior
- interfaces introduced before real pressure exists
- placeholder profiles with no meaningful recipes

Adversarial response:
- reject scaffolding that does not improve a real vertical slice

### Drift pattern: warnings instead of refusals
Symptoms:
- weak candidates survive because "it is only a warning"
- rationale becomes a dumping ground for uncertainty

Adversarial response:
- ask whether the output should exist at all
- prefer refusal unless the warning is truly bounded and understandable

### Drift pattern: compatibility optimism
Symptoms:
- docs say "Prometheus-compatible"
- code quietly assumes Prometheus-specific behaviors everywhere

Adversarial response:
- make support assumptions concrete in tests and docs

### Drift pattern: debug paths become product paths
Symptoms:
- inspect-only shortcuts leak into generation
- non-strict exploratory behavior becomes the default in practice

Adversarial response:
- guard exploratory modes tightly
- keep strict, safe defaults dominant

## 7. Concrete Failure Scenarios
Use these as hostile thought experiments and future tests.

### Scenario 1: huge cardinality hidden behind innocent metric names
Example shape:
- metric names look standard
- labels include `user_id` or `session_id`
- the query template groups by the wrong dimension

Question:
- does DashGen refuse it, downgrade it, or emit a trap?

### Scenario 2: inconsistent metadata across backend endpoints
Example shape:
- HELP and TYPE metadata missing for some metrics
- histogram family partially present
- suffixes suggest one thing, runtime behavior suggests another

Question:
- does the classifier become conservative, or fabricate certainty?

### Scenario 3: backend timeout during execution validation
Example shape:
- parse succeeds
- selector looks sane
- execution validation times out intermittently

Question:
- does the system fail closed, or does it quietly pass weak queries?

### Scenario 4: stable IDs break after harmless refactor
Example shape:
- no product-level behavior changed
- internal key material changed order
- panel IDs churn across the whole dashboard

Question:
- what tests catch this before users do?

### Scenario 5: profile sections disappear unexpectedly
Example shape:
- one fixture update slightly changes metric inventory
- recipe thresholds flip
- a whole dashboard section vanishes

Question:
- is that a justified omission or unstable threshold behavior?

### Scenario 6: unofficial backend mostly works
Example shape:
- discovery APIs respond
- parser accepts expressions
- execution semantics differ subtly

Question:
- how does DashGen avoid claiming correctness it cannot guarantee?

## 8. Adversarial Questions By Component
Use these when reviewing code.

### Discovery
- What exact API calls are assumed?
- What happens when metadata is partial?
- Are discovery limits bounded?
- Are missing fields normalized or guessed?

### Inventory
- Is label ordering canonicalized everywhere?
- Are inferred fields clearly distinguished from source truth?
- Can provenance be inspected later?

### Classification
- Which heuristics are authoritative?
- What happens when suffixes conflict with metadata?
- Are histogram families detected conservatively enough?

### Recipes
- Why does this recipe exist?
- What evidence says it is safe for v0.1?
- What concrete metric shapes must match before it activates?

### Validation
- Is parse success being mistaken for semantic correctness?
- Are timeouts and transient HTTP errors clearly handled?
- Are budgets enforced globally or only per query?

### Safety
- Are risky labels centralized in one policy source?
- What stops a result-set explosion?
- Is the downgrade/refuse boundary explicit?

### Rendering
- Is output ordering canonical?
- Are Grafana-specific details leaking backward into IR?
- Can renderer refactors churn goldens without semantic change?

### IDs
- What exact canonical material forms stable IDs?
- Will harmless refactors churn them?
- Are there tests specifically designed to catch that?

## 9. Evidence Thresholds
Do not accept comfort-language such as:
- "should be deterministic"
- "probably safe"
- "works on my fixture"
- "Prometheus-compatible enough"

Accept only evidence such as:
- explicit tests
- fixture coverage
- golden stability
- documented thresholds
- refusal paths exercised in tests

If a claim matters to trust, it needs evidence in code or fixtures.

## 10. Adversarial Review Checklist
Before accepting a meaningful change, ask:

- What new ambiguity did this introduce?
- What unsafe query could now slip through?
- What output ordering could now churn?
- What fallback path is now more permissive than intended?
- What future abstraction was added without current need?
- What fixture would fail if this assumption were wrong?
- Can a user tell why a panel was omitted?
- If the backend lies, does DashGen become conservative or optimistic?

## 11. Red Flags
Treat these as immediate review concerns:

- "We can tighten this later"
- "It only affects warnings"
- "The backend usually behaves"
- "We need this abstraction for future providers"
- "The JSON changed, but only cosmetically"
- "This skips validation just in this one path"
- "This profile is incomplete now but we scaffolded it"

These are common beginnings of trust erosion.

## 12. Required Counter-Pressure
To keep DashGen firm, every major feature should face counter-pressure:

### For new recipes
- require fixture evidence
- require refusal cases
- require rationale examples

### For safety changes
- require explicit before and after policy explanation
- require tests for unsafe regressions

### For renderer changes
- require golden review
- require proof that churn is structural or intentional

### For architecture changes
- require explanation of why the simpler design is insufficient
- require evidence that complexity buys real product value now

## 13. Final Adversarial Rule
If a decision makes DashGen look more complete but less truthful, less stable, or less reviewable, reject it.

The enemy is not missing features.
The enemy is confident-looking output that users should not trust.
