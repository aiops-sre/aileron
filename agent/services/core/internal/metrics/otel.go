package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// otelCollector exports metrics via OpenTelemetry OTLP to any compatible
// collector: Grafana Alloy, OpenTelemetry Collector, Honeycomb, Datadog, etc.
//
// Configure via env vars (standard OTel SDK conventions):
//   OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
//   OTEL_EXPORTER_OTLP_PROTOCOL=grpc          # or "http"
//   OTEL_SERVICE_NAME=kubesense-agent
//   OTEL_EXPORTER_OTLP_HEADERS=x-api-key=abc  # comma-separated key=value
type otelCollector struct {
	provider *metric.MeterProvider
	meter    otelmetric.Meter

	healthEvents    otelmetric.Int64Counter
	incidents       otelmetric.Int64Counter
	chaosScore      otelmetric.Float64Gauge
	violations      otelmetric.Int64Counter
	apmSignal       otelmetric.Float64Gauge
	rcaDuration     otelmetric.Float64Histogram
	bufferLen       otelmetric.Int64Gauge
}

func newOTelCollector(cfg Config) (*otelCollector, error) {
	if cfg.OTLPEndpoint == "" {
		return nil, fmt.Errorf("metrics: OTEL_EXPORTER_OTLP_ENDPOINT is required for otel backend")
	}

	ctx := context.Background()

	var exporter metric.Exporter
	var err error

	switch cfg.OTLPProtocol {
	case "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.OTLPEndpoint),
			otlpmetrichttp.WithInsecure(),
		}
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.OTLPHeaders))
		}
		exporter, err = otlpmetrichttp.New(ctx, opts...)
	default: // grpc
		dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		if len(cfg.OTLPHeaders) > 0 {
			md := metadata.New(cfg.OTLPHeaders)
			dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(
				func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
					return invoker(metadata.NewOutgoingContext(ctx, md), method, req, reply, cc, opts...)
				},
			))
		}
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlpmetricgrpc.WithDialOption(dialOpts...),
			otlpmetricgrpc.WithInsecure(),
		}
		exporter, err = otlpmetricgrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("metrics: otel exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("aileron.component", "kubesense-agent"),
		),
	)
	if err != nil {
		res = resource.Default()
	}

	provider := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exporter,
			metric.WithInterval(30*time.Second),
		)),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(provider)

	meter := provider.Meter("aileron/kubesense")

	c := &otelCollector{provider: provider, meter: meter}
	if err := c.initInstruments(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *otelCollector) initInstruments() error {
	var err error

	if c.healthEvents, err = c.meter.Int64Counter("kubesense.health_events",
		otelmetric.WithDescription("Total K8s health events processed"),
		otelmetric.WithUnit("{event}"),
	); err != nil {
		return err
	}

	if c.incidents, err = c.meter.Int64Counter("kubesense.incidents",
		otelmetric.WithDescription("Total RCA incidents by phase transition"),
		otelmetric.WithUnit("{incident}"),
	); err != nil {
		return err
	}

	if c.chaosScore, err = c.meter.Float64Gauge("kubesense.chaos_readiness_score",
		otelmetric.WithDescription("Chaos readiness score 0-100 per namespace. 100 = fully ready."),
		otelmetric.WithUnit("1"),
	); err != nil {
		return err
	}

	if c.violations, err = c.meter.Int64Counter("kubesense.config_violations",
		otelmetric.WithDescription("Total config policy violations detected"),
		otelmetric.WithUnit("{violation}"),
	); err != nil {
		return err
	}

	if c.apmSignal, err = c.meter.Float64Gauge("kubesense.apm_signal",
		otelmetric.WithDescription("APM golden signal value (latency_p99, error_rate, saturation, throughput)"),
	); err != nil {
		return err
	}

	if c.rcaDuration, err = c.meter.Float64Histogram("kubesense.rca_duration",
		otelmetric.WithDescription("RCA investigation duration in seconds"),
		otelmetric.WithUnit("s"),
	); err != nil {
		return err
	}

	if c.bufferLen, err = c.meter.Int64Gauge("kubesense.correlation_buffer_len",
		otelmetric.WithDescription("Current correlation sliding window buffer size"),
		otelmetric.WithUnit("{event}"),
	); err != nil {
		return err
	}

	return nil
}

func (c *otelCollector) RecordHealthEvent(ctx context.Context, clusterID, eventType, severity string) {
	c.healthEvents.Add(ctx, 1,
		otelmetric.WithAttributes(
			attribute.String("cluster_id", clusterID),
			attribute.String("event_type", eventType),
			attribute.String("severity", severity),
		),
	)
}

func (c *otelCollector) RecordIncident(ctx context.Context, clusterID, incidentType, phase string) {
	c.incidents.Add(ctx, 1,
		otelmetric.WithAttributes(
			attribute.String("cluster_id", clusterID),
			attribute.String("incident_type", incidentType),
			attribute.String("phase", phase),
		),
	)
}

func (c *otelCollector) RecordChaosScore(ctx context.Context, clusterID, namespace string, score float64) {
	c.chaosScore.Record(ctx, score,
		otelmetric.WithAttributes(
			attribute.String("cluster_id", clusterID),
			attribute.String("namespace", namespace),
		),
	)
}

func (c *otelCollector) RecordConfigViolation(ctx context.Context, clusterID, namespace, ruleID, severity string) {
	c.violations.Add(ctx, 1,
		otelmetric.WithAttributes(
			attribute.String("cluster_id", clusterID),
			attribute.String("namespace", namespace),
			attribute.String("rule_id", ruleID),
			attribute.String("severity", severity),
		),
	)
}

func (c *otelCollector) RecordAPMSignal(ctx context.Context, clusterID, namespace, service, signal string, value float64) {
	c.apmSignal.Record(ctx, value,
		otelmetric.WithAttributes(
			attribute.String("cluster_id", clusterID),
			attribute.String("namespace", namespace),
			attribute.String("service", service),
			attribute.String("signal", signal),
		),
	)
}

func (c *otelCollector) RecordRCADuration(ctx context.Context, clusterID string, seconds float64) {
	c.rcaDuration.Record(ctx, seconds,
		otelmetric.WithAttributes(attribute.String("cluster_id", clusterID)),
	)
}

func (c *otelCollector) RecordBufferLen(ctx context.Context, clusterID string, length int64) {
	c.bufferLen.Record(ctx, length,
		otelmetric.WithAttributes(attribute.String("cluster_id", clusterID)),
	)
}

func (c *otelCollector) Flush(ctx context.Context) error {
	return c.provider.ForceFlush(ctx)
}

func (c *otelCollector) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.provider.Shutdown(ctx)
}
