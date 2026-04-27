package recipes

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"dashgen/internal/ir"
	"dashgen/internal/profiles"
)

// captureLogger records every Warnf call so tests can assert on the
// emitted messages.
type captureLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureLogger) Warnf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, fmt.Sprintf(format, args...))
}

func (c *captureLogger) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// =============================================================================
// TestRegistry_OverrideEmitsWarning (T12)
// =============================================================================

func TestRegistry_OverrideEmitsWarning(t *testing.T) {
	pr := NewProfileRegistries()
	cap := &captureLogger{}
	pr.WithLogger(cap)

	// Built-in service_http_rate is already registered by NewProfileRegistries
	// (via NewServiceRegistry → NewServiceHTTPRate). Loading the YAML
	// fixture under SourceUser should override it and log a WARN.
	loaded := loadFixture(t, "service_http_rate.yaml")
	loaded.Source = SourceUser // ensure user-source path

	if err := pr.RegisterFromLoaded([]LoadedRecipe{loaded}); err != nil {
		t.Fatalf("RegisterFromLoaded: %v", err)
	}

	// User wins.
	got := pr.Service.ByName("service_http_rate")
	if got == nil {
		t.Fatal("override missing from Service registry")
	}
	if _, ok := got.(*YAMLRecipe); !ok {
		t.Errorf("ByName returned %T, want *YAMLRecipe", got)
	}

	// WARN was emitted.
	msgs := cap.all()
	if len(msgs) == 0 {
		t.Fatal("expected at least one Warnf call")
	}
	combined := strings.Join(msgs, "\n")
	if !strings.Contains(combined, "service_http_rate") {
		t.Errorf("warn message should mention recipe name: %q", combined)
	}
	if !strings.Contains(combined, "overrides built-in") {
		t.Errorf("warn message should mention built-in override: %q", combined)
	}
}

// =============================================================================
// TestRegistry_ProfileBinding (T17)
// =============================================================================

func TestRegistry_ProfileBinding(t *testing.T) {
	pr := NewProfileRegistries()

	loaded := loadFixture(t, "service_http_rate.yaml")
	if err := pr.RegisterFromLoaded([]LoadedRecipe{loaded}); err != nil {
		t.Fatalf("RegisterFromLoaded: %v", err)
	}

	// Present in service registry.
	if got := pr.Service.ByName("service_http_rate"); got == nil {
		t.Errorf("service_http_rate missing from Service registry")
	}
	// Absent from other-profile registries.
	if got := pr.Infra.ByName("service_http_rate"); got != nil {
		t.Errorf("service_http_rate leaked into Infra registry")
	}
	if got := pr.K8s.ByName("service_http_rate"); got != nil {
		t.Errorf("service_http_rate leaked into K8s registry")
	}

	// For() routes correctly.
	if pr.For(profiles.ProfileService) != pr.Service {
		t.Errorf("For(service) returned wrong registry")
	}
	if pr.For(profiles.ProfileInfra) != pr.Infra {
		t.Errorf("For(infra) returned wrong registry")
	}
	if pr.For(profiles.ProfileK8s) != pr.K8s {
		t.Errorf("For(k8s) returned wrong registry")
	}
	if pr.For(profiles.Profile("nonsense")) != nil {
		t.Errorf("For(unknown) should return nil")
	}
}

// =============================================================================
// TestRegistry_DuplicateUserRejected
// =============================================================================

func TestRegistry_DuplicateUserRejected(t *testing.T) {
	pr := NewProfileRegistries()

	loaded := loadFixture(t, "service_http_rate.yaml")
	loaded.Source = SourceUser

	dup := loaded // value copy, same Spec/Source
	dup.Path = loaded.Path + ".dup"

	err := pr.RegisterFromLoaded([]LoadedRecipe{loaded, dup})
	if err == nil {
		t.Fatal("expected ErrDuplicateRecipe")
	}
	var dupErr *ErrDuplicateRecipe
	if !errors.As(err, &dupErr) {
		t.Fatalf("got %T, want *ErrDuplicateRecipe: %v", err, err)
	}
	if dupErr.Name != "service_http_rate" {
		t.Errorf("Name=%q, want service_http_rate", dupErr.Name)
	}
	if dupErr.Profile != "service" {
		t.Errorf("Profile=%q, want service", dupErr.Profile)
	}
	if dupErr.PathA == "" || dupErr.PathB == "" {
		t.Errorf("PathA/PathB should be populated: %+v", dupErr)
	}
}

// =============================================================================
// TestRegistry_ByName_Replace exercises the new Registry methods directly.
// =============================================================================

func TestRegistry_ByName_Replace(t *testing.T) {
	r := NewServiceRegistry()
	if got := r.ByName("service_http_rate"); got == nil {
		t.Fatal("expected built-in service_http_rate via ByName")
	}
	if got := r.ByName("does_not_exist"); got != nil {
		t.Errorf("ByName(missing) = %v, want nil", got)
	}

	// Replace with a fake recipe and verify it sticks.
	fake := &fakeRecipe{name: "service_http_rate", section: "traffic"}
	r.Replace(fake)
	got := r.ByName("service_http_rate")
	if got != fake {
		t.Errorf("Replace did not install the new recipe; got %T", got)
	}

	// Replace on a non-existent name falls back to Register.
	novel := &fakeRecipe{name: "novel_recipe", section: "overview"}
	r.Replace(novel)
	if r.ByName("novel_recipe") != novel {
		t.Errorf("Replace fallback to Register failed for novel_recipe")
	}
}

// fakeRecipe is a no-op Recipe used by TestRegistry_ByName_Replace.
type fakeRecipe struct {
	name    string
	section string
}

func (f *fakeRecipe) Name() string                     { return f.name }
func (f *fakeRecipe) Section() string                  { return f.section }
func (f *fakeRecipe) Match(_ ClassifiedMetricView) bool { return false }
func (f *fakeRecipe) BuildPanels(_ ClassifiedInventorySnapshot, _ profiles.Profile) []ir.Panel {
	return nil
}
