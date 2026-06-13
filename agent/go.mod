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

	// Prometheus metrics — pinned to Go 1.22 compatible version
	github.com/prometheus/client_golang v1.19.1

	// OpenTelemetry — metrics export via OTLP (Grafana Alloy, OTel Collector, etc.)
	go.opentelemetry.io/otel                                    v1.28.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v0.50.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v0.50.0
	go.opentelemetry.io/otel/metric                             v1.28.0
	go.opentelemetry.io/otel/sdk                                v1.28.0
	go.opentelemetry.io/otel/sdk/metric                         v1.28.0
	google.golang.org/grpc                                      v1.65.0
)
