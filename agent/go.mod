module github.com/aileron-platform/aileron/agent

go 1.22

require (
	// Kubernetes client-go and API
	k8s.io/api          v0.30.0
	k8s.io/apimachinery v0.30.0
	k8s.io/client-go    v0.30.0

	// Kafka — same library as AlertHub (Strimzi compatibility)
	github.com/IBM/sarama v1.43.0

	// UUID generation
	github.com/google/uuid v1.6.0

	// PostgreSQL driver — used by collector and core
	github.com/lib/pq v1.10.9

	// Prometheus metrics for RCA correlator — pinned to Go 1.22 compatible version.
	// v1.23.x requires Go >= 1.23; this pin prevents -mod=mod from auto-fetching it.
	github.com/prometheus/client_golang v1.19.1
)
