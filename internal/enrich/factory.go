// Provider factory for the v0.2 enrichment seam.
//
// The factory is the single extension point for new providers. Adding a
// provider — local (e.g. ollama, llama.cpp, lm-studio) or hosted (e.g.
// anthropic, openai) — requires exactly two changes inside this package:
//
//  1. A new file (e.g. anthropic.go) containing a Constructor that builds
//     and returns the concrete Enricher.
//  2. A single Register call in that file's init(), keyed by the provider
//     name as the user types it on the command line.
//
// Nothing outside this package needs to change. internal/app/generate
// calls New(Spec{Provider: cfg.Provider, ...}) and gets back an Enricher
// (or a clear error) — it does not switch on provider names.
//
// This factory is *intentionally* light: it does not own retry policy,
// per-provider auth, model selection beyond the Spec field, or audit
// logging. Those belong inside each Constructor so cross-provider concerns
// stay decoupled and one provider's complexity cannot leak into another.
package enrich

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownProvider is returned by New when the requested provider name
// has no Constructor registered. Callers may wrap with their own context
// (app/generate wraps it as ErrBackend).
var ErrUnknownProvider = errors.New("enrich: unknown provider")

// ErrNotImplementedYet is returned by Constructors that exist as
// placeholders for not-yet-shipped providers. The presence of a
// placeholder is the signal that the boundary is shaped to accept the
// provider — only the body is missing. Callers can errors.Is on this
// sentinel to distinguish "you typed a provider name we recognise but
// haven't built yet" from "you typed something we have never heard of".
var ErrNotImplementedYet = errors.New("enrich: provider not implemented yet")

// Spec is the minimum input a Constructor needs to build an Enricher.
// Cross-provider knobs (cache directory, cache bypass) live here so
// callers do not have to assemble provider-specific configs. Provider-
// specific options (e.g. an Anthropic model id) belong on the
// Constructor's own argument or environment, not on this struct, so
// adding a provider does not balloon Spec.
type Spec struct {
	// Provider is the user-typed provider name. Empty string is treated
	// identically to "off" so the zero value of Spec is a safe noop.
	Provider string

	// Model overrides the provider's default model id. Empty means
	// "use the provider default". Ignored by the noop provider.
	Model string

	// CacheDir is the on-disk cache directory passed to providers that
	// participate in inventory-hash caching. Empty means "use the
	// default" (~/.cache/dashgen/enrich). Ignored by the noop provider.
	CacheDir string

	// NoCache, when true, instructs the provider to bypass any cache hit
	// and force a fresh request. Used for authoring/debugging only.
	// Ignored by the noop provider.
	NoCache bool
}

// Constructor builds an Enricher from a Spec. Implementations live in
// sibling files in this package and call Register at init() time.
type Constructor func(Spec) (Enricher, error)

// registry holds Constructors keyed by user-facing provider name. It is
// populated solely by Register calls from init() functions in this
// package. The map is never read concurrently with writes because all
// writes happen during package init before any New call can fire.
var registry = map[string]Constructor{}

// Register adds a Constructor for a provider name. Last-init-wins: a
// later Register call for the same name overwrites the earlier entry,
// which is what tests want when substituting a fake provider. Production
// code should never re-register a built-in name.
func Register(name string, ctor Constructor) {
	if name == "" {
		panic("enrich.Register: provider name cannot be empty")
	}
	if ctor == nil {
		panic("enrich.Register: constructor cannot be nil")
	}
	registry[name] = ctor
}

// Providers returns the sorted list of registered provider names. Used
// by CLI help, error messages, and audit tooling.
func Providers() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// New constructs an Enricher for the requested provider. The empty
// string and "off" are equivalent; both return NoopEnricher with no
// error. Unknown provider names return ErrUnknownProvider; placeholder
// providers return an error wrapping ErrNotImplementedYet.
func New(spec Spec) (Enricher, error) {
	name := spec.Provider
	if name == "" {
		name = "off"
	}
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownProvider, name, Providers())
	}
	return ctor(spec)
}

// Built-in registrations. The "off" and "noop" entries return the always-
// safe NoopEnricher. The "openai" entry is a placeholder stub that returns
// ErrNotImplementedYet so adding the real provider is a one-file change in
// this package.
//
// The "anthropic" name is registered by anthropic.go's init() — not here —
// because Go runs package init() functions in filename order and the real
// constructor must not be overwritten by a placeholder later in the
// alphabet. Keeping anthropic out of this init body is what makes that
// override deterministic.
//
// The "ollama" placeholder is intentionally retained even though local
// AI is deferred to the v0.3 backlog (see ROADMAP.md). Keeping the
// registration shape means dropping in a local provider later — ollama,
// llama.cpp, lm-studio, or any other — is a single file plus a one-line
// edit to this init body.
func init() {
	noop := func(Spec) (Enricher, error) { return NewNoopEnricher(), nil }
	Register("off", noop)
	Register("noop", noop)
	Register("openai", func(Spec) (Enricher, error) {
		return nil, fmt.Errorf("%w: openai (lands in v0.2 Phase 3)", ErrNotImplementedYet)
	})
	Register("ollama", func(Spec) (Enricher, error) {
		return nil, fmt.Errorf("%w: ollama (deferred to v0.3 backlog)", ErrNotImplementedYet)
	})
}
