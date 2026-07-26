package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	spanAttributeValueLengthLimit   = 128
	spanAttributeCountLimit         = 32
	spanProcessorMaxQueueSize       = 128
	spanProcessorMaxExportBatchSize = 32
)

type (
	exporterFactory       func(context.Context, Config) (sdktrace.SpanExporter, error)
	metricExporterFactory func(context.Context, Config) (sdkmetric.Exporter, error)
)

// Runtime owns the OpenTelemetry tracing and metric providers plus propagation settings.
type Runtime struct {
	provider      *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	propagator    propagation.TraceContext
	shutdownOnce  sync.Once
	shutdownErr   error
}

// New creates a tracing runtime using the supplied export configuration.
func New(ctx context.Context, config Config) (*Runtime, error) {
	return newRuntimeWithMetricExporter(ctx, config, newOTLPGRPCExporter, newOTLPGRPCMetricExporter)
}

func newRuntime(ctx context.Context, config Config, createExporter exporterFactory) (*Runtime, error) {
	return newRuntimeWithMetricExporter(ctx, config, createExporter, newOTLPGRPCMetricExporter)
}

func newRuntimeWithMetricExporter(ctx context.Context, config Config, createExporter exporterFactory, createMetricExporter metricExporterFactory) (*Runtime, error) {
	config = config.normalized()
	if err := config.validate(); err != nil {
		return nil, err
	}

	res, err := runtimeResource(ctx)
	if err != nil {
		return nil, err
	}

	spanLimits := sdktrace.NewSpanLimits()
	spanLimits.AttributeValueLengthLimit = spanAttributeValueLengthLimit
	spanLimits.AttributeCountLimit = spanAttributeCountLimit
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithResource(res),
		sdktrace.WithRawSpanLimits(spanLimits),
	}
	if config.Endpoint != "" {
		exporter, err := createExporter(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		options = append(options, sdktrace.WithBatcher(
			exporter,
			sdktrace.WithMaxQueueSize(spanProcessorMaxQueueSize),
			sdktrace.WithMaxExportBatchSize(spanProcessorMaxExportBatchSize),
		))
	}
	meterOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if config.MetricsEndpoint != "" {
		exporter, err := createMetricExporter(ctx, config)
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		meterOptions = append(meterOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)))
	} else {
		meterOptions = append(meterOptions, sdkmetric.WithReader(sdkmetric.NewManualReader()))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)
	otel.SetMeterProvider(meterProvider)
	return &Runtime{provider: sdktrace.NewTracerProvider(options...), meterProvider: meterProvider, propagator: propagation.TraceContext{}}, nil
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

func newOTLPGRPCMetricExporter(ctx context.Context, config Config) (sdkmetric.Exporter, error) {
	exporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(config.MetricsEndpoint))
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC metric exporter: %w", err)
	}
	return exporter, nil
}

// TracerProvider returns the explicit provider for application instrumentation.
func (runtime *Runtime) TracerProvider() trace.TracerProvider {
	return runtime.provider
}

// MeterProvider returns the explicit provider for application metrics instrumentation.
func (runtime *Runtime) MeterProvider() metric.MeterProvider {
	return runtime.meterProvider
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

// Shutdown flushes and closes runtime telemetry resources using ctx as its bound.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	runtime.shutdownOnce.Do(func() {
		runtime.shutdownErr = errors.Join(runtime.provider.Shutdown(ctx), runtime.meterProvider.Shutdown(ctx))
	})
	return runtime.shutdownErr
}

// TraceID returns the current valid W3C trace identifier, or an empty string.
func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
