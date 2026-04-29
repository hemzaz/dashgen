// Command metrics-emitter is a synthetic Prometheus exporter used by the
// dashgen e2e test harness. It exposes one or more series for every metric
// name that the 47 built-in dashgen recipes match against, with realistic
// label sets and types so the classifier infers the correct trait set.
//
// Static values are deliberate: dashgen's validation pipeline only needs
// /metadata + /labels + a non-empty instant-query result, which `rate()` over
// a constant counter still satisfies after two scrape intervals.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	reg := prometheus.NewRegistry()
	register(reg)

	addr := os.Getenv("EMITTER_ADDR")
	if addr == "" {
		addr = ":9091"
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	log.Printf("metrics-emitter listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// register builds and seeds every metric required to fire all 47 recipes.
// Each helper sets a value once at startup; rate() over the static counters
// still produces non-NaN data once Prometheus has at least two samples.
func register(reg *prometheus.Registry) {
	registerService(reg)
	registerInfra(reg)
	registerK8s(reg)
}

// ----------------------------------------------------------------------
// service profile (22 recipes)
// ----------------------------------------------------------------------

func registerService(reg *prometheus.Registry) {
	// service_http_rate / service_http_errors:
	// Counter with HTTP-shape labels (method, route, status_code) -> trait service_http.
	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests by route and status code.",
	}, []string{"method", "route", "status_code"})
	reg.MustRegister(httpRequests)
	httpRequests.WithLabelValues("GET", "/api/users", "200").Add(100)
	httpRequests.WithLabelValues("POST", "/api/users", "201").Add(50)
	httpRequests.WithLabelValues("GET", "/api/users", "500").Add(2)

	// service_client_http: counter with "client" in name + status_code label.
	httpClient := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_client_requests_total",
		Help: "Total outbound HTTP client requests.",
	}, []string{"host", "status_code"})
	reg.MustRegister(httpClient)
	httpClient.WithLabelValues("upstream-a", "200").Add(80)
	httpClient.WithLabelValues("upstream-a", "503").Add(1)

	// service_http_latency: histogram with HTTP labels -> service_http + latency_histogram.
	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP server request duration distribution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	reg.MustRegister(httpDuration)
	httpDuration.WithLabelValues("GET", "/api/users").Observe(0.05)
	httpDuration.WithLabelValues("GET", "/api/users").Observe(0.25)
	httpDuration.WithLabelValues("POST", "/api/users").Observe(0.15)

	// service_request_size / service_response_size: histograms with method/handler.
	httpReqSize := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_size_bytes",
		Help:    "HTTP request body size distribution.",
		Buckets: []float64{100, 1000, 10000, 100000, 1000000},
	}, []string{"method", "handler"})
	reg.MustRegister(httpReqSize)
	httpReqSize.WithLabelValues("POST", "/api/users").Observe(512)
	httpReqSize.WithLabelValues("POST", "/api/users").Observe(2048)

	httpRespSize := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_response_size_bytes",
		Help:    "HTTP response body size distribution.",
		Buckets: []float64{100, 1000, 10000, 100000, 1000000},
	}, []string{"method", "handler"})
	reg.MustRegister(httpRespSize)
	httpRespSize.WithLabelValues("GET", "/api/users").Observe(1024)
	httpRespSize.WithLabelValues("GET", "/api/users").Observe(4096)

	// service_grpc_rate / service_grpc_errors: counter with grpc_* labels.
	grpcHandled := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "grpc_server_handled_total",
		Help: "Total gRPC server handled calls.",
	}, []string{"grpc_method", "grpc_service", "grpc_type", "grpc_code"})
	reg.MustRegister(grpcHandled)
	grpcHandled.WithLabelValues("GetUser", "users.UserService", "unary", "OK").Add(200)
	grpcHandled.WithLabelValues("GetUser", "users.UserService", "unary", "Internal").Add(3)

	// service_grpc_latency: histogram with grpc_* labels and _seconds suffix.
	grpcLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_server_handling_seconds",
		Help:    "gRPC server handling duration distribution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"grpc_method", "grpc_service"})
	reg.MustRegister(grpcLatency)
	grpcLatency.WithLabelValues("GetUser", "users.UserService").Observe(0.01)
	grpcLatency.WithLabelValues("GetUser", "users.UserService").Observe(0.08)

	// service_cpu: process_cpu_seconds_total counter (no extra labels).
	processCPU := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "process_cpu_seconds_total",
		Help: "Total user+system CPU time spent in seconds.",
	})
	reg.MustRegister(processCPU)
	processCPU.Add(12.5)

	// service_memory: process_resident_memory_bytes gauge.
	processMem := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_resident_memory_bytes",
		Help: "Resident memory size in bytes.",
	})
	reg.MustRegister(processMem)
	processMem.Set(123456789)

	// service_goroutines: go_goroutines gauge.
	goRoutines := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_goroutines",
		Help: "Number of goroutines that currently exist.",
	})
	reg.MustRegister(goRoutines)
	goRoutines.Set(42)

	// service_gc_pause: go_gc_duration_seconds summary with quantile label.
	gcPause := prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "go_gc_duration_seconds",
		Help:       "A summary of the pause duration of garbage collection cycles.",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	})
	reg.MustRegister(gcPause)
	gcPause.Observe(0.0001)
	gcPause.Observe(0.0002)
	gcPause.Observe(0.0005)

	// service_db_query_latency: histogram with db_query in name (no http/grpc labels).
	dbQuery := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "db_query_duration_seconds",
		Help:    "Database query duration distribution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"query_type"})
	reg.MustRegister(dbQuery)
	dbQuery.WithLabelValues("select").Observe(0.005)
	dbQuery.WithLabelValues("insert").Observe(0.012)

	// service_db_pool_go_sql_stats: gauge pair _in_use / _max.
	goSQLInUse := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_sql_stats_connections_in_use",
		Help: "go-sql connections currently in use.",
	})
	reg.MustRegister(goSQLInUse)
	goSQLInUse.Set(3)
	goSQLMax := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "go_sql_stats_connections_max",
		Help: "go-sql max open connections.",
	})
	reg.MustRegister(goSQLMax)
	goSQLMax.Set(10)

	// service_db_pool_pgxpool: gauge pair acquired / max.
	pgxAcquired := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pgxpool_acquired_connections",
		Help: "pgx pool acquired connections.",
	})
	reg.MustRegister(pgxAcquired)
	pgxAcquired.Set(4)
	pgxMax := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pgxpool_max_connections",
		Help: "pgx pool max connections.",
	})
	reg.MustRegister(pgxMax)
	pgxMax.Set(20)

	// service_cache_hits: counter pair <prefix>_cache_hits_total / _cache_misses_total.
	cacheHits := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "redis_cache_hits_total",
		Help: "Redis cache hits.",
	})
	reg.MustRegister(cacheHits)
	cacheHits.Add(900)
	cacheMisses := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "redis_cache_misses_total",
		Help: "Redis cache misses.",
	})
	reg.MustRegister(cacheMisses)
	cacheMisses.Add(100)

	// service_job_success: counter pair _jobs_succeeded_total / _jobs_failed_total.
	jobsOK := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_jobs_succeeded_total",
		Help: "Successful background jobs.",
	}, []string{"queue"})
	reg.MustRegister(jobsOK)
	jobsOK.WithLabelValues("default").Add(500)
	jobsFail := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_jobs_failed_total",
		Help: "Failed background jobs.",
	}, []string{"queue"})
	reg.MustRegister(jobsFail)
	jobsFail.WithLabelValues("default").Add(5)

	// service_kafka_consumer_lag: gauge.
	kafkaLag := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kafka_consumergroup_lag",
		Help: "Kafka consumer group lag per partition.",
	}, []string{"consumergroup", "topic", "partition"})
	reg.MustRegister(kafkaLag)
	kafkaLag.WithLabelValues("group-a", "events", "0").Set(123)
	kafkaLag.WithLabelValues("group-a", "events", "1").Set(45)

	// service_tls_expiry: gauge with _cert_expiry_timestamp_seconds suffix.
	tlsExpiry := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "probe_cert_expiry_timestamp_seconds",
		Help: "Probe TLS certificate expiry as a Unix timestamp.",
	}, []string{"target"})
	reg.MustRegister(tlsExpiry)
	tlsExpiry.WithLabelValues("api.example.com").Set(2000000000)
}

// ----------------------------------------------------------------------
// infra profile (14 recipes)
// ----------------------------------------------------------------------

func registerInfra(reg *prometheus.Registry) {
	// infra_cpu: node_cpu_seconds_total counter.
	nodeCPU := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_cpu_seconds_total",
		Help: "Seconds the cpus spent in each mode.",
	}, []string{"cpu", "mode"})
	reg.MustRegister(nodeCPU)
	for _, cpu := range []string{"0", "1"} {
		for _, mode := range []string{"user", "system", "idle", "iowait"} {
			nodeCPU.WithLabelValues(cpu, mode).Add(1234)
		}
	}

	// infra_memory: gauge pair MemAvailable / MemTotal.
	memAvail := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_memory_MemAvailable_bytes",
		Help: "Memory information field MemAvailable_bytes.",
	})
	reg.MustRegister(memAvail)
	memAvail.Set(4 * 1024 * 1024 * 1024)
	memTotal := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_memory_MemTotal_bytes",
		Help: "Memory information field MemTotal_bytes.",
	})
	reg.MustRegister(memTotal)
	memTotal.Set(8 * 1024 * 1024 * 1024)

	// infra_disk + infra_filesystem_usage: filesystem avail/size pair.
	fsAvail := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_filesystem_avail_bytes",
		Help: "Filesystem space available to non-root users in bytes.",
	}, []string{"device", "fstype", "mountpoint"})
	reg.MustRegister(fsAvail)
	fsAvail.WithLabelValues("/dev/sda1", "ext4", "/").Set(50 * 1024 * 1024 * 1024)
	fsSize := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "node_filesystem_size_bytes",
		Help: "Filesystem size in bytes.",
	}, []string{"device", "fstype", "mountpoint"})
	reg.MustRegister(fsSize)
	fsSize.WithLabelValues("/dev/sda1", "ext4", "/").Set(100 * 1024 * 1024 * 1024)

	// infra_disk_iops: reads_completed + writes_completed counters.
	diskReads := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_disk_reads_completed_total",
		Help: "The total number of reads completed successfully.",
	}, []string{"device"})
	reg.MustRegister(diskReads)
	diskReads.WithLabelValues("sda").Add(10000)
	diskWrites := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_disk_writes_completed_total",
		Help: "The total number of writes completed successfully.",
	}, []string{"device"})
	reg.MustRegister(diskWrites)
	diskWrites.WithLabelValues("sda").Add(5000)

	// infra_disk_io_latency: io_time + io_time_weighted counters.
	diskIOTime := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_disk_io_time_seconds_total",
		Help: "Total seconds spent doing IOs.",
	}, []string{"device"})
	reg.MustRegister(diskIOTime)
	diskIOTime.WithLabelValues("sda").Add(123)
	diskIOWeighted := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_disk_io_time_weighted_seconds_total",
		Help: "Total weighted seconds spent doing IOs.",
	}, []string{"device"})
	reg.MustRegister(diskIOWeighted)
	diskIOWeighted.WithLabelValues("sda").Add(345)

	// infra_network_receive / infra_network_transmit: bytes counters.
	netRX := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_network_receive_bytes_total",
		Help: "Network device statistic receive_bytes.",
	}, []string{"device"})
	reg.MustRegister(netRX)
	netRX.WithLabelValues("eth0").Add(1e9)
	netTX := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_network_transmit_bytes_total",
		Help: "Network device statistic transmit_bytes.",
	}, []string{"device"})
	reg.MustRegister(netTX)
	netTX.WithLabelValues("eth0").Add(5e8)

	// infra_nic_errors: receive_errs + transmit_drop counters.
	nicErrs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_network_receive_errs_total",
		Help: "Network device statistic receive_errs.",
	}, []string{"device"})
	reg.MustRegister(nicErrs)
	nicErrs.WithLabelValues("eth0").Add(2)
	nicDrops := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_network_transmit_drop_total",
		Help: "Network device statistic transmit_drop.",
	}, []string{"device"})
	reg.MustRegister(nicDrops)
	nicDrops.WithLabelValues("eth0").Add(1)

	// infra_load: node_load1 / node_load5 / node_load15 gauges.
	for name, val := range map[string]float64{
		"node_load1":  0.5,
		"node_load5":  0.4,
		"node_load15": 0.3,
	} {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: name + " load average."})
		reg.MustRegister(g)
		g.Set(val)
	}

	// infra_file_descriptors: process_open_fds + process_max_fds gauges.
	openFDs := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_open_fds",
		Help: "Number of open file descriptors.",
	})
	reg.MustRegister(openFDs)
	openFDs.Set(20)
	maxFDs := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "process_max_fds",
		Help: "Maximum number of open file descriptors.",
	})
	reg.MustRegister(maxFDs)
	maxFDs.Set(1024)

	// infra_conntrack: gauge pair entries / entries_limit.
	conntrack := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_nf_conntrack_entries",
		Help: "Number of currently allocated conntrack entries.",
	})
	reg.MustRegister(conntrack)
	conntrack.Set(1024)
	conntrackLimit := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_nf_conntrack_entries_limit",
		Help: "Maximum size of connection tracking table.",
	})
	reg.MustRegister(conntrackLimit)
	conntrackLimit.Set(65536)

	// infra_interrupts: node_interrupts_total counter.
	interrupts := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "node_interrupts_total",
		Help: "Total number of interrupts.",
	}, []string{"cpu", "type"})
	reg.MustRegister(interrupts)
	interrupts.WithLabelValues("0", "TIMER").Add(50000)
	interrupts.WithLabelValues("1", "TIMER").Add(50000)

	// infra_ntp_offset: gauge.
	ntpOffset := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "node_timex_offset_seconds",
		Help: "Time offset in between local system and reference clock.",
	})
	reg.MustRegister(ntpOffset)
	ntpOffset.Set(0.001)
}

// ----------------------------------------------------------------------
// k8s profile (12 recipes)
// ----------------------------------------------------------------------

func registerK8s(reg *prometheus.Registry) {
	// k8s_apiserver_latency: apiserver_request_duration_seconds histogram.
	apiserverLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "apiserver_request_duration_seconds",
		Help:    "Response latency distribution for each verb, dry run, group, version, subresource.",
		Buckets: prometheus.DefBuckets,
	}, []string{"verb", "resource"})
	reg.MustRegister(apiserverLatency)
	apiserverLatency.WithLabelValues("GET", "pods").Observe(0.01)
	apiserverLatency.WithLabelValues("LIST", "pods").Observe(0.05)
	apiserverLatency.WithLabelValues("GET", "configmaps").Observe(0.02)

	// k8s_container_cpu: counter with container/namespace/pod labels.
	containerCPU := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "container_cpu_usage_seconds_total",
		Help: "Cumulative cpu time consumed in seconds.",
	}, []string{"container", "namespace", "pod"})
	reg.MustRegister(containerCPU)
	containerCPU.WithLabelValues("app", "default", "app-abc").Add(123.4)
	containerCPU.WithLabelValues("sidecar", "default", "app-abc").Add(45.6)

	// k8s_container_memory: gauge with container/namespace/pod labels.
	containerMem := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "container_memory_working_set_bytes",
		Help: "Current working set in bytes.",
	}, []string{"container", "namespace", "pod"})
	reg.MustRegister(containerMem)
	containerMem.WithLabelValues("app", "default", "app-abc").Set(150 * 1024 * 1024)
	containerMem.WithLabelValues("sidecar", "default", "app-abc").Set(50 * 1024 * 1024)

	// k8s_coredns: histogram + counter, both with server/zone labels.
	corednsLat := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "coredns_dns_request_duration_seconds",
		Help:    "Histogram of the time each request took.",
		Buckets: prometheus.DefBuckets,
	}, []string{"server", "zone"})
	reg.MustRegister(corednsLat)
	corednsLat.WithLabelValues("dns://:53", "cluster.local.").Observe(0.001)
	corednsLat.WithLabelValues("dns://:53", "cluster.local.").Observe(0.005)
	corednsReq := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "coredns_dns_requests_total",
		Help: "Counter of DNS requests made per zone, protocol and family.",
	}, []string{"server", "zone"})
	reg.MustRegister(corednsReq)
	corednsReq.WithLabelValues("dns://:53", "cluster.local.").Add(1000)

	// k8s_deployment_availability: gauge pair status_replicas_available / spec_replicas.
	depAvail := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_deployment_status_replicas_available",
		Help: "The number of available replicas per deployment.",
	}, []string{"namespace", "deployment"})
	reg.MustRegister(depAvail)
	depAvail.WithLabelValues("default", "app").Set(3)
	depSpec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_deployment_spec_replicas",
		Help: "Number of desired pods for a deployment.",
	}, []string{"namespace", "deployment"})
	reg.MustRegister(depSpec)
	depSpec.WithLabelValues("default", "app").Set(3)

	// k8s_etcd_commit: etcd_disk_backend_commit_duration_seconds histogram.
	etcdCommit := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "etcd_disk_backend_commit_duration_seconds",
		Help:    "The latency distributions of commit called by backend.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
	}, []string{})
	reg.MustRegister(etcdCommit)
	etcdCommit.WithLabelValues().Observe(0.005)
	etcdCommit.WithLabelValues().Observe(0.012)

	// k8s_hpa_scaling: gauge pair current_replicas / desired_replicas.
	hpaCurrent := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_horizontalpodautoscaler_status_current_replicas",
		Help: "Current number of replicas of pods managed by this autoscaler.",
	}, []string{"namespace", "horizontalpodautoscaler"})
	reg.MustRegister(hpaCurrent)
	hpaCurrent.WithLabelValues("default", "app-hpa").Set(3)
	hpaDesired := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_horizontalpodautoscaler_status_desired_replicas",
		Help: "Desired number of replicas of pods managed by this autoscaler.",
	}, []string{"namespace", "horizontalpodautoscaler"})
	reg.MustRegister(hpaDesired)
	hpaDesired.WithLabelValues("default", "app-hpa").Set(5)

	// k8s_node_conditions: gauge with node/condition/status.
	nodeCond := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_node_status_condition",
		Help: "The condition of a cluster node.",
	}, []string{"node", "condition", "status"})
	reg.MustRegister(nodeCond)
	for _, cond := range []string{"NotReady", "MemoryPressure", "DiskPressure", "PIDPressure"} {
		nodeCond.WithLabelValues("node-1", cond, "true").Set(0)
		nodeCond.WithLabelValues("node-1", cond, "false").Set(1)
	}

	// k8s_oom_kills: kube_pod_container_status_terminated_reason gauge.
	oomKills := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_pod_container_status_terminated_reason",
		Help: "Describes the reason the container is currently in terminated state.",
	}, []string{"container", "namespace", "pod", "reason"})
	reg.MustRegister(oomKills)
	oomKills.WithLabelValues("app", "default", "app-xyz", "OOMKilled").Set(1)
	oomKills.WithLabelValues("app", "default", "app-xyz", "Error").Set(0)

	// k8s_pod_health: kube_pod_status_phase gauge.
	podPhase := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_pod_status_phase",
		Help: "The pods current phase.",
	}, []string{"namespace", "phase", "pod"})
	reg.MustRegister(podPhase)
	for _, phase := range []string{"Running", "Pending", "Failed", "Succeeded"} {
		podPhase.WithLabelValues("default", phase, "app-abc").Set(0)
	}
	podPhase.WithLabelValues("default", "Running", "app-abc").Set(1)

	// k8s_pvc_usage: gauge pair available_bytes / capacity_bytes.
	pvcAvail := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubelet_volume_stats_available_bytes",
		Help: "Number of available bytes in the volume.",
	}, []string{"namespace", "persistentvolumeclaim"})
	reg.MustRegister(pvcAvail)
	pvcAvail.WithLabelValues("default", "data-app").Set(10 * 1024 * 1024 * 1024)
	pvcCap := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubelet_volume_stats_capacity_bytes",
		Help: "Capacity in bytes of the volume.",
	}, []string{"namespace", "persistentvolumeclaim"})
	reg.MustRegister(pvcCap)
	pvcCap.WithLabelValues("default", "data-app").Set(20 * 1024 * 1024 * 1024)

	// k8s_restarts: counter.
	restarts := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_pod_container_status_restarts_total",
		Help: "The number of container restarts per container.",
	}, []string{"container", "namespace", "pod"})
	reg.MustRegister(restarts)
	restarts.WithLabelValues("app", "default", "app-abc").Add(2)

	// k8s_scheduler_latency: histogram with result label.
	schedLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "scheduler_scheduling_attempt_duration_seconds",
		Help:    "Scheduling attempt latency in seconds (scheduling algorithm + binding).",
		Buckets: prometheus.DefBuckets,
	}, []string{"result"})
	reg.MustRegister(schedLatency)
	schedLatency.WithLabelValues("scheduled").Observe(0.005)
	schedLatency.WithLabelValues("scheduled").Observe(0.020)
	schedLatency.WithLabelValues("unschedulable").Observe(0.100)
}
