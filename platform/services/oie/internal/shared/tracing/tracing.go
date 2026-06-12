package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	jaeger "go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const tracerName = "github.com/aileron-platform/aileron/platform/services/oie"

// Tracer returns the OIE tracer instance.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// TracerConfig holds Jaeger exporter configuration.
type TracerConfig struct {
	ServiceName    string
	ServiceVersion string
	// JaegerEndpoint is the Jaeger collector HTTP endpoint.
	// Example: "http://jaeger-collector.platform-services:14268/api/traces"
	// When empty, a no-op tracer is used (no spans exported).
	JaegerEndpoint string
	Environment    string
}

// InitTracer sets up the OpenTelemetry trace provider.
// Returns a shutdown function that must be called on service exit.
func InitTracer(ctx context.Context, cfg TracerConfig) (func(context.Context) error, error) {
	if cfg.JaegerEndpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(
		jaeger.WithEndpoint(cfg.JaegerEndpoint),
	))
	if err != nil {
		return nil, fmt.Errorf("creating Jaeger exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating trace resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
