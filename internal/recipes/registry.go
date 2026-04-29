package recipes

import (
	"fmt"
	"sort"

	"dashgen/internal/profiles"
)

// Registry is the sorted collection of recipes available to synthesis.
//
// Determinism: recipes are stored sorted by Name so that tie-breaking during
// synthesis is reproducible across runs.
type Registry struct {
	recipes []Recipe
}

// NewRegistry returns an empty registry. Prefer NewServiceRegistry for the
// v0.1 service profile so callers get the full set of recipes in one call.
func NewRegistry() *Registry {
	return &Registry{}
}

// NewServiceRegistry returns a registry preloaded with every recipe in scope
// for the service profile. HTTP + gRPC RPC families + process saturation.
//
//   - service_http_rate / service_http_errors / service_http_latency   (v0.1)
//   - service_cpu / service_memory                                      (v0.1)
//   - service_grpc_rate / service_grpc_errors / service_grpc_latency   (v0.2)
//   - service_goroutines / service_gc_pause                            (v0.2 Go runtime)
//   - service_db_query_latency / service_tls_expiry / service_cache_hits (v0.2 Tier-2)
//   - service_job_success                                               (v0.2 Tier-2)
//   - service_request_size / service_response_size                     (v0.2 Tier-2 stragglers)
func NewServiceRegistry() *Registry {
	r := NewRegistry()
	// service_http_rate migrated to YAML (T4A.1) — see data/service/service_http_rate.yaml.
	// service_http_errors migrated to YAML (T5.0.C) — see data/service/service_http_errors.yaml.
	// service_http_latency migrated to YAML (T5.0.A) — see data/service/service_http_latency.yaml.
	// service_cpu migrated to YAML (T4A.1) — see data/service/service_cpu.yaml.
	// service_memory migrated to YAML (T4A.1) — see data/service/service_memory.yaml.
	// service_grpc_rate migrated to YAML (T1B.1) — see data/service/service_grpc_rate.yaml.
	// service_grpc_errors migrated to YAML (T5.1) — see data/service/service_grpc_errors.yaml.
	// service_grpc_latency migrated to YAML (T5.1 continuation) — see data/service/service_grpc_latency.yaml.
	// service_goroutines migrated to YAML (T1B.1) — see data/service/service_goroutines.yaml.
	// service_gc_pause migrated to YAML (T6A.1) — see data/service/service_gc_pause.yaml.
	// service_db_query_latency migrated to YAML (T6A.1) — see data/service/service_db_query_latency.yaml.
	// service_tls_expiry migrated to YAML (T5.1) — see data/service/service_tls_expiry.yaml.
	r.Register(NewServiceCacheHits())
	r.Register(NewServiceJobSuccess())
	// service_client_http migrated to YAML (T6A.1) — see data/service/service_client_http.yaml.
	r.Register(NewServiceDBPool())
	// service_kafka_consumer_lag migrated to YAML (T5.1) — see data/service/service_kafka_consumer_lag.yaml.
	// service_request_size migrated to YAML (T5.1 continuation) — see data/service/service_request_size.yaml.
	// service_response_size migrated to YAML (T5.1 continuation) — see data/service/service_response_size.yaml.
	LoadBuiltinYAMLs(r, "service")
	return r
}

// NewInfraRegistry returns a registry preloaded with every recipe in scope
// for the infra profile (node_exporter shape).
//
//   - infra_cpu / infra_memory / infra_disk / infra_network   (v0.1)
//   - infra_load / infra_filesystem_usage                     (v0.2)
//   - infra_file_descriptors / infra_nic_errors               (v0.2)
//   - infra_conntrack / infra_disk_iops / infra_disk_io_latency (v0.2 Tier-2)
//   - infra_interrupts                                          (v0.2 Tier-2 stragglers)
func NewInfraRegistry() *Registry {
	r := NewRegistry()
	// infra_cpu migrated to YAML (T1B.1) — see data/infra/infra_cpu.yaml.
	r.Register(NewInfraMemory())
	r.Register(NewInfraDisk())
	r.Register(NewInfraNetwork())
	// infra_load migrated to YAML (T4A.1) — see data/infra/infra_load.yaml.
	r.Register(NewInfraFilesystemUsage())
	r.Register(NewInfraFileDescriptors())
	// infra_nic_errors migrated to YAML (T5.0.B) — see data/infra/infra_nic_errors.yaml.
	r.Register(NewInfraConntrack())
	// infra_disk_iops migrated to YAML (T5.1) — see data/infra/infra_disk_iops.yaml.
	// infra_disk_io_latency migrated to YAML (T5.0.D) — see data/infra/infra_disk_io_latency.yaml.
	// infra_ntp_offset migrated to YAML (T1B.1) — see data/infra/infra_ntp_offset.yaml.
	// infra_interrupts migrated to YAML (T4A.1) — see data/infra/infra_interrupts.yaml.
	LoadBuiltinYAMLs(r, "infra")
	return r
}

// NewK8sRegistry returns a registry preloaded with every recipe in scope for
// the k8s profile (kube-state-metrics + cAdvisor shape).
//
//   - k8s_pod_health / k8s_container_resources / k8s_restarts   (v0.1)
//   - k8s_deployment_availability / k8s_node_conditions         (v0.2)
//   - k8s_pvc_usage / k8s_oom_kills                             (v0.2)
//   - k8s_apiserver_latency                                     (v0.2 Tier-2)
//   - k8s_scheduler_latency / k8s_coredns                       (v0.2 Tier-2 stragglers)
func NewK8sRegistry() *Registry {
	r := NewRegistry()
	// k8s_pod_health migrated to YAML (T1B.1) — see data/k8s/k8s_pod_health.yaml.
	r.Register(NewK8sContainerResources())
	// k8s_restarts migrated to YAML (T4A.1) — see data/k8s/k8s_restarts.yaml.
	r.Register(NewK8sDeploymentAvailability())
	r.Register(NewK8sNodeConditions())
	r.Register(NewK8sPVCUsage())
	// k8s_oom_kills migrated to YAML (T4A.1) — see data/k8s/k8s_oom_kills.yaml.
	// k8s_apiserver_latency migrated to YAML (T5.1 continuation) — see data/k8s/k8s_apiserver_latency.yaml.
	// k8s_etcd_commit migrated to YAML (T5.1 continuation) — see data/k8s/k8s_etcd_commit.yaml.
	r.Register(NewK8sHPAScaling())
	// k8s_scheduler_latency migrated to YAML (T5.1 continuation) — see data/k8s/k8s_scheduler_latency.yaml.
	r.Register(NewK8sCoreDNS())
	LoadBuiltinYAMLs(r, "k8s")
	return r
}

// Register adds a recipe and keeps the internal slice sorted by name. Safe
// to call multiple times with distinct recipes.
func (r *Registry) Register(rec Recipe) {
	if r == nil || rec == nil {
		return
	}
	r.recipes = append(r.recipes, rec)
	// Sort by Name ascending: this is the authoritative tie-break order for
	// synth. Two recipes with the same name would collide on UID inputs, so
	// names must be unique and the sort stays stable.
	sort.Slice(r.recipes, func(i, j int) bool {
		return r.recipes[i].Name() < r.recipes[j].Name()
	})
}

// All returns the recipes in stable sorted order. The returned slice is a
// copy so callers cannot mutate the registry.
func (r *Registry) All() []Recipe {
	if r == nil {
		return nil
	}
	out := make([]Recipe, len(r.recipes))
	copy(out, r.recipes)
	return out
}

// ByName returns the registered recipe whose Name() matches name, or nil
// if no such recipe is registered. Used by the v0.3 override flow to
// detect collisions between built-in and user recipes.
func (r *Registry) ByName(name string) Recipe {
	if r == nil {
		return nil
	}
	for _, rec := range r.recipes {
		if rec.Name() == name {
			return rec
		}
	}
	return nil
}

// Replace swaps in rec in place of any existing recipe with the same
// Name(). If no existing recipe matches, Replace falls back to Register.
// The internal sort order is preserved (replacement at the same index).
//
// This is the in-place override hook used by ProfileRegistries when a
// user YAML recipe shadows a built-in (DSL §10, T12).
func (r *Registry) Replace(rec Recipe) {
	if r == nil || rec == nil {
		return
	}
	name := rec.Name()
	for i, existing := range r.recipes {
		if existing.Name() == name {
			r.recipes[i] = rec
			return
		}
	}
	r.Register(rec)
}

// =============================================================================
// v0.3 — Profile-aware merge of built-in + user-loaded recipes
// =============================================================================

// ErrDuplicateRecipe is returned when two same-source recipes share a
// name within the same profile. User → user collisions are errors;
// built-in → user is an override (logged WARN, not error).
type ErrDuplicateRecipe struct {
	Name    string
	Profile string
	PathA   string // first registration's path
	PathB   string // second registration's path
}

// Error returns the canonical message format. Tests pin against the
// "duplicate recipe" prefix.
func (e *ErrDuplicateRecipe) Error() string {
	return fmt.Sprintf(
		"duplicate recipe %q in profile %q: %s vs %s",
		e.Name, e.Profile, e.PathA, e.PathB,
	)
}

// ProfileRegistries holds one Registry per profile (service, infra, k8s)
// and is the v0.3 integration point for built-in + user YAML recipe
// loading. The three Registry fields are pre-populated with the existing
// Go-implementing-Recipe recipes via NewProfileRegistries; v0.3 user
// recipes are added on top via RegisterFromLoaded.
type ProfileRegistries struct {
	Service *Registry
	Infra   *Registry
	K8s     *Registry

	// sources records the source label per (profile, name) so we can
	// detect overrides. Recipe is an interface and has no Source method;
	// this map is the side channel.
	sources map[profileNameKey]string
	paths   map[profileNameKey]string

	logger Logger
}

type profileNameKey struct {
	profile string
	name    string
}

// NewProfileRegistries returns a ProfileRegistries pre-loaded with all
// built-in Go recipes (preserving v0.2 behavior). Use WithLogger to
// attach a logger before calling RegisterFromLoaded so override warnings
// reach the user.
func NewProfileRegistries() *ProfileRegistries {
	return &ProfileRegistries{
		Service: NewServiceRegistry(),
		Infra:   NewInfraRegistry(),
		K8s:     NewK8sRegistry(),
		sources: map[profileNameKey]string{},
		paths:   map[profileNameKey]string{},
		logger:  nopLogger{},
	}
}

// WithLogger attaches l for override-warning emission. Returns the
// receiver so the call is chainable.
func (pr *ProfileRegistries) WithLogger(l Logger) *ProfileRegistries {
	if l != nil {
		pr.logger = l
	}
	return pr
}

// For returns the registry for profile p. Returns nil for an unknown
// profile so callers can branch defensively.
func (pr *ProfileRegistries) For(p profiles.Profile) *Registry {
	if pr == nil {
		return nil
	}
	switch p {
	case profiles.ProfileService:
		return pr.Service
	case profiles.ProfileInfra:
		return pr.Infra
	case profiles.ProfileK8s:
		return pr.K8s
	}
	return nil
}

// RegisterFromLoaded converts each LoadedRecipe into a *YAMLRecipe and
// registers it in the appropriate profile's registry, applying override
// + duplicate semantics per DSL §10:
//
//   - Built-in already registered + new is user → log WARN, replace (T12).
//   - Same-source duplicate (user→user, builtin→builtin) → ErrDuplicateRecipe.
//   - Different profiles, same name → both register (profile is part of identity).
//   - Unknown profile → error.
//
// Built-in Go recipes registered via NewServiceRegistry/etc. are NOT
// tracked in the sources map (they predate v0.3). When a user YAML
// recipe arrives with a name that matches a built-in Go recipe, the
// missing source entry is treated as SourceBuiltin — so the user wins
// via the override path with a WARN.
//
// Stops on the first error; partial registrations from prior loop
// iterations remain visible (callers should treat any error as a
// signal to abort the run).
func (pr *ProfileRegistries) RegisterFromLoaded(loaded []LoadedRecipe) error {
	if pr == nil {
		return fmt.Errorf("recipes: nil ProfileRegistries")
	}
	for _, l := range loaded {
		rec, err := NewYAMLRecipe(l)
		if err != nil {
			return err
		}

		profStr := l.Spec.Metadata.Profile
		target := pr.For(profiles.Profile(profStr))
		if target == nil {
			return fmt.Errorf("recipe %s: unknown profile %q", l.Spec.Metadata.Name, profStr)
		}

		key := profileNameKey{profile: profStr, name: l.Spec.Metadata.Name}
		existing := target.ByName(l.Spec.Metadata.Name)
		existingSource := pr.sources[key]

		if existing != nil {
			// If the existing entry isn't tracked in sources, it's a
			// pre-v0.3 Go recipe registered via NewServiceRegistry/etc.
			// Treat as built-in.
			if existingSource == "" {
				existingSource = SourceBuiltin
			}
			switch {
			case existingSource == SourceBuiltin && l.Source == SourceUser:
				pr.logger.Warnf(
					"recipe %q (%s): user recipe at %s overrides built-in",
					l.Spec.Metadata.Name, profStr, l.Path,
				)
				target.Replace(rec)
			case existingSource == l.Source:
				return &ErrDuplicateRecipe{
					Name:    l.Spec.Metadata.Name,
					Profile: profStr,
					PathA:   pr.paths[key],
					PathB:   l.Path,
				}
			default:
				// existingSource=user + new=builtin: per DSL §9.1 builtin
				// always loads first, so this branch is unreachable in
				// normal flow. Defensive: replace.
				target.Replace(rec)
			}
		} else {
			target.Register(rec)
		}

		pr.sources[key] = l.Source
		pr.paths[key] = l.Path
	}
	return nil
}
