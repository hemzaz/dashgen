package recipes

import "sort"

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
func NewServiceRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewServiceHTTPRate())
	r.Register(NewServiceHTTPErrors())
	r.Register(NewServiceHTTPLatency())
	r.Register(NewServiceCPU())
	r.Register(NewServiceMemory())
	r.Register(NewServiceGRPCRate())
	r.Register(NewServiceGRPCErrors())
	r.Register(NewServiceGRPCLatency())
	r.Register(NewServiceGoroutines())
	r.Register(NewServiceGCPause())
	r.Register(NewServiceDBQueryLatency())
	r.Register(NewServiceTLSExpiry())
	r.Register(NewServiceCacheHits())
	r.Register(NewServiceJobSuccess())
	r.Register(NewServiceClientHTTP())
	r.Register(NewServiceDBPool())
	r.Register(NewServiceKafkaConsumerLag())
	return r
}

// NewInfraRegistry returns a registry preloaded with every recipe in scope
// for the infra profile (node_exporter shape).
//
//   - infra_cpu / infra_memory / infra_disk / infra_network   (v0.1)
//   - infra_load / infra_filesystem_usage                     (v0.2)
//   - infra_file_descriptors / infra_nic_errors               (v0.2)
//   - infra_conntrack / infra_disk_iops / infra_disk_io_latency (v0.2 Tier-2)
func NewInfraRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewInfraCPU())
	r.Register(NewInfraMemory())
	r.Register(NewInfraDisk())
	r.Register(NewInfraNetwork())
	r.Register(NewInfraLoad())
	r.Register(NewInfraFilesystemUsage())
	r.Register(NewInfraFileDescriptors())
	r.Register(NewInfraNICErrors())
	r.Register(NewInfraConntrack())
	r.Register(NewInfraDiskIOPS())
	r.Register(NewInfraDiskIOLatency())
	r.Register(NewInfraNTPOffset())
	return r
}

// NewK8sRegistry returns a registry preloaded with every recipe in scope for
// the k8s profile (kube-state-metrics + cAdvisor shape).
//
//   - k8s_pod_health / k8s_container_resources / k8s_restarts   (v0.1)
//   - k8s_deployment_availability / k8s_node_conditions         (v0.2)
//   - k8s_pvc_usage / k8s_oom_kills                             (v0.2)
//   - k8s_apiserver_latency                                     (v0.2 Tier-2)
func NewK8sRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewK8sPodHealth())
	r.Register(NewK8sContainerResources())
	r.Register(NewK8sRestarts())
	r.Register(NewK8sDeploymentAvailability())
	r.Register(NewK8sNodeConditions())
	r.Register(NewK8sPVCUsage())
	r.Register(NewK8sOOMKills())
	r.Register(NewK8sApiserverLatency())
	r.Register(NewK8sEtcdCommit())
	r.Register(NewK8sHPAScaling())
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
