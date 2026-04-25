package enrich

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestNew_NoopAliases is the load-bearing assertion for the v0.2 default:
// any of "", "off", or "noop" must yield a NoopEnricher with no error.
func TestNew_NoopAliases(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"", "off", "noop"} {
		provider := provider
		t.Run("provider="+labelFor(provider), func(t *testing.T) {
			t.Parallel()
			got, err := New(Spec{Provider: provider})
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", provider, err)
			}
			if _, ok := got.(*NoopEnricher); !ok {
				t.Fatalf("New(%q) returned %T; want *NoopEnricher", provider, got)
			}
			desc := got.Describe()
			if desc.Provider != "noop" {
				t.Errorf("Describe().Provider = %q; want %q", desc.Provider, "noop")
			}
			if !desc.Offline {
				t.Errorf("Describe().Offline = false; noop must report offline=true")
			}
		})
	}
}

// TestNew_PlaceholderProviders confirms the placeholder Constructors for
// providers we have shaped the boundary for but not yet built. Each must
// return an error wrapping ErrNotImplementedYet so callers can
// errors.Is-distinguish "we recognised this name" from "we have never
// heard of this name".
func TestNew_PlaceholderProviders(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai", "ollama"} {
		provider := provider
		t.Run("provider="+provider, func(t *testing.T) {
			t.Parallel()
			got, err := New(Spec{Provider: provider})
			if got != nil {
				t.Errorf("New(%q) returned non-nil enricher; placeholder must return nil", provider)
			}
			if err == nil {
				t.Fatalf("New(%q) returned nil error; want ErrNotImplementedYet wrap", provider)
			}
			if !errors.Is(err, ErrNotImplementedYet) {
				t.Errorf("New(%q) error chain missing ErrNotImplementedYet: %v", provider, err)
			}
			if !strings.Contains(err.Error(), provider) {
				t.Errorf("New(%q) error must name the provider; got %v", provider, err)
			}
		})
	}
}

// TestNew_UnknownProvider asserts that a provider name with no
// registration produces ErrUnknownProvider, distinct from the
// "registered-but-not-implemented" case above. Error message must list
// the known providers so the user can self-correct.
func TestNew_UnknownProvider(t *testing.T) {
	t.Parallel()
	got, err := New(Spec{Provider: "definitely-not-real"})
	if got != nil {
		t.Errorf("New(unknown) returned non-nil enricher; want nil")
	}
	if err == nil {
		t.Fatal("New(unknown) returned nil error; want ErrUnknownProvider wrap")
	}
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("error chain missing ErrUnknownProvider: %v", err)
	}
	// Unknown should NOT be confused with not-implemented-yet; this is
	// the load-bearing distinction the registry provides.
	if errors.Is(err, ErrNotImplementedYet) {
		t.Errorf("unknown-provider error must not also report not-implemented: %v", err)
	}
	for _, want := range []string{"off", "noop", "anthropic", "openai", "ollama"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must list registered provider %q; got %v", want, err)
		}
	}
}

// TestProviders_SortedDeterministic guards the registry's listing
// behavior — CLI help and audit tooling depend on a stable order.
func TestProviders_SortedDeterministic(t *testing.T) {
	t.Parallel()
	got := Providers()
	want := []string{"anthropic", "noop", "off", "ollama", "openai"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Providers() = %v; want %v", got, want)
	}
}

// TestRegister_PanicsOnEmptyName guards the contract that Register
// rejects malformed registrations at init() time rather than producing
// a hard-to-debug "" key in the registry.
func TestRegister_PanicsOnEmptyName(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(\"\", ...) did not panic")
		}
	}()
	Register("", func(Spec) (Enricher, error) { return NewNoopEnricher(), nil })
}

// TestRegister_PanicsOnNilConstructor guards the same contract for the
// other malformed-input case.
func TestRegister_PanicsOnNilConstructor(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(name, nil) did not panic")
		}
	}()
	Register("test-nil-ctor", nil)
}

// TestRegister_LastInitWinsForTestSubstitution covers the documented
// "last-init-wins" behavior: a test can substitute a real provider with
// a fake one for a single test, then restore. We verify the override
// applies and then restore the original so this test stays hermetic
// against the rest of the suite.
func TestRegister_LastInitWinsForTestSubstitution(t *testing.T) {
	// Not parallel: mutates the package-global registry under a unique
	// name. The deferred delete restores the registry for any sibling
	// test that calls Providers() later.
	const name = "test-override"
	defer delete(registry, name)

	Register(name, func(Spec) (Enricher, error) {
		return nil, errors.New("first")
	})
	Register(name, func(Spec) (Enricher, error) {
		return NewNoopEnricher(), nil
	})

	got, err := New(Spec{Provider: name})
	if err != nil {
		t.Fatalf("override registration not honored: %v", err)
	}
	if _, ok := got.(*NoopEnricher); !ok {
		t.Errorf("got %T; want *NoopEnricher (last registration wins)", got)
	}
}

func labelFor(s string) string {
	if s == "" {
		return "empty"
	}
	return s
}
