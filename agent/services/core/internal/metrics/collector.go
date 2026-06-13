// Package metrics provides a unified interface for push-based metrics emission
// from the KubeSense agent. Two adapters are available:
//   - PrometheusAdapter: exposes a /metrics scrape endpoint and a Pushgateway push path
//   - OTelAdapter: exports via OpenTelemetry OTLP (gRPC or HTTP) to any OTel collector
//
// Usage:
//
//	col, err := metrics.New(metrics.ConfigFromEnv())
//	col.RecordHealthEvent(ctx, clusterID, eventType, severity)
//	col.RecordIncident(ctx, clusterID, incidentType, phase)
//	col.RecordChaosScore(ctx, clusterID, namespace, score)
package metrics

import (
	"context"
	"os"
	"strings"
)

// Collector is the unified metrics interface both adapters implement.
type Collector interface {
	// RecordHealthEvent increments the health event counter.
	RecordHealthEvent(ctx context.Context, clusterID, eventType, severity string)
	// RecordIncident records a state transition for a KubeSense incident.
	RecordIncident(ctx context.Context, clusterID, incidentType, phase string)
	// RecordChaosScore records the latest chaos readiness score (0–100) for a namespace.
	RecordChaosScore(ctx context.Context, clusterID, namespace string, score float64)
	// RecordConfigViolation increments the config violation counter.
	RecordConfigViolation(ctx context.Context, clusterID, namespace, ruleID, severity string)
	// RecordAPMSignal records an APM golden signal value.
	// signal is one of: latency_p99, error_rate, saturation, throughput
	RecordAPMSignal(ctx context.Context, clusterID, namespace, service, signal string, value float64)
	// RecordRCADuration records the time taken for an RCA investigation in seconds.
	RecordRCADuration(ctx context.Context, clusterID string, seconds float64)
	// RecordBufferLen records the current correlation buffer length.
	RecordBufferLen(ctx context.Context, clusterID string, length int64)
	// Flush forces any buffered metrics to be sent. No-op for scrape-based adapters.
	Flush(ctx context.Context) error
	// Close shuts down the collector cleanly.
	Close() error
}

// Backend selects which adapter to instantiate.
type Backend string

const (
	BackendPrometheus Backend = "prometheus"
	BackendOTel       Backend = "otel"
	BackendNoop       Backend = "noop"
)

// Config holds common configuration for both adapters.
type Config struct {
	Backend Backend

	// Prometheus-specific
	// METRICS_LISTEN_ADDR — address to expose /metrics scrape endpoint (default ":9090")
	ListenAddr string
	// PUSHGATEWAY_URL — if set, metrics are also pushed to this Prometheus Pushgateway
	PushgatewayURL string
	// PUSHGATEWAY_JOB — Pushgateway job label (default "kubesense-agent")
	PushgatewayJob string

	// OTel-specific
	// OTEL_EXPORTER_OTLP_ENDPOINT — OTLP gRPC endpoint (e.g. "otel-collector:4317")
	OTLPEndpoint string
	// OTEL_EXPORTER_OTLP_PROTOCOL — "grpc" (default) or "http"
	OTLPProtocol string
	// OTEL_SERVICE_NAME — service.name resource attribute (default "kubesense-agent")
	ServiceName string
	// OTEL_EXPORTER_OTLP_HEADERS — comma-separated "key=value" pairs for auth headers
	OTLPHeaders map[string]string
}

// ConfigFromEnv builds a Config from environment variables.
func ConfigFromEnv() Config {
	backend := Backend(strings.ToLower(envOr("METRICS_BACKEND", "prometheus")))
	return Config{
		Backend:        backend,
		ListenAddr:     envOr("METRICS_LISTEN_ADDR", ":9090"),
		PushgatewayURL: os.Getenv("PUSHGATEWAY_URL"),
		PushgatewayJob: envOr("PUSHGATEWAY_JOB", "kubesense-agent"),
		OTLPEndpoint:   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OTLPProtocol:   envOr("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),
		ServiceName:    envOr("OTEL_SERVICE_NAME", "kubesense-agent"),
		OTLPHeaders:    parseHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
	}
}

// New creates a Collector from config. Falls back to noop if backend is unknown.
func New(cfg Config) (Collector, error) {
	switch cfg.Backend {
	case BackendPrometheus:
		return newPrometheusCollector(cfg)
	case BackendOTel:
		return newOTelCollector(cfg)
	default:
		return &noopCollector{}, nil
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseHeaders(raw string) map[string]string {
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return out
}

// noopCollector silently discards all metrics — used when METRICS_BACKEND=noop.
type noopCollector struct{}

func (n *noopCollector) RecordHealthEvent(_ context.Context, _, _, _ string)              {}
func (n *noopCollector) RecordIncident(_ context.Context, _, _, _ string)                 {}
func (n *noopCollector) RecordChaosScore(_ context.Context, _, _ string, _ float64)       {}
func (n *noopCollector) RecordConfigViolation(_ context.Context, _, _, _, _ string)       {}
func (n *noopCollector) RecordAPMSignal(_ context.Context, _, _, _, _ string, _ float64)  {}
func (n *noopCollector) RecordRCADuration(_ context.Context, _ string, _ float64)         {}
func (n *noopCollector) RecordBufferLen(_ context.Context, _ string, _ int64)             {}
func (n *noopCollector) Flush(_ context.Context) error                                     { return nil }
func (n *noopCollector) Close() error                                                       { return nil }
