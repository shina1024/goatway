package telemetry

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

type exporterFactory func(context.Context, Config) (sdktrace.SpanExporter, error)

// Runtime owns the OpenTelemetry tracing provider and propagation settings.
type Runtime struct {
	provider   *sdktrace.TracerProvider
	propagator propagation.TraceContext
}

// New creates a tracing runtime using the supplied export configuration.
func New(ctx context.Context, config Config) (*Runtime, error) {
	return newRuntime(ctx, config, newOTLPGRPCExporter)
}

func newRuntime(ctx context.Context, config Config, createExporter exporterFactory) (*Runtime, error) {
	config = config.normalized()
	if err := config.validate(); err != nil {
		return nil, err
	}

	res, err := runtimeResource(ctx)
	if err != nil {
		return nil, err
	}

	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
	}
	if config.Endpoint != "" {
		exporter, err := createExporter(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		options = append(options, sdktrace.WithBatcher(exporter))
	}

	return &Runtime{
		provider:   sdktrace.NewTracerProvider(options...),
		propagator: propagation.TraceContext{},
	}, nil
}

func runtimeResource(ctx context.Context) (*resource.Resource, error) {
	environment, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, fmt.Errorf("read OpenTelemetry resource environment: %w", err)
	}
	res, err := resource.Merge(
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("goatway")),
		environment,
	)
	if err != nil {
		return nil, fmt.Errorf("merge OpenTelemetry resource: %w", err)
	}
	return res, nil
}

func newOTLPGRPCExporter(ctx context.Context, config Config) (sdktrace.SpanExporter, error) {
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(config.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC exporter: %w", err)
	}
	return exporter, nil
}

// TracerProvider returns the explicit provider for application instrumentation.
func (runtime *Runtime) TracerProvider() trace.TracerProvider {
	return runtime.provider
}

// TraceContext returns the W3C Trace Context propagator used by this runtime.
func (runtime *Runtime) TraceContext() propagation.TraceContext {
	return runtime.propagator
}

// HTTPHandler wraps next with the runtime's server tracing configuration.
func (runtime *Runtime) HTTPHandler(next http.Handler) http.Handler {
	return otelhttp.NewHandler(
		next,
		"goatway.request",
		otelhttp.WithTracerProvider(runtime.provider),
		otelhttp.WithPropagators(runtime.propagator),
		otelhttp.WithSpanNameFormatter(func(operation string, _ *http.Request) string {
			return operation
		}),
	)
}

// Shutdown flushes and closes runtime tracing resources using ctx as its bound.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	return runtime.provider.Shutdown(ctx)
}

// TraceID returns the current valid W3C trace identifier, or an empty string.
func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
