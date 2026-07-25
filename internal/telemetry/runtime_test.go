package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestRuntime_createsValidTraceIDWhenExportIsDisabled(t *testing.T) {
	// Given
	runtime, err := New(context.Background(), Config{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })

	// When
	spanContext, span := runtime.TracerProvider().Tracer("telemetry-test").Start(context.Background(), "test.span")
	span.End()

	// Then
	require.Regexp(t, "^[0-9a-f]{32}$", TraceID(spanContext))
}

func TestRuntime_doesNotCreateExporterWhenExportIsDisabled(t *testing.T) {
	// Given
	var factoryCalls int

	// When
	runtime, err := newRuntime(context.Background(), Config{}, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		factoryCalls++
		return nil, errors.New("exporter factory must not be called")
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })

	// Then
	require.Zero(t, factoryCalls)
}

func TestRuntime_returnsConfiguredExporterInitializationError(t *testing.T) {
	// Given
	wantErr := errors.New("exporter unavailable")
	config := Config{Endpoint: "https://collector.example.test:4317", Protocol: grpcProtocol}

	// When
	_, err := newRuntime(context.Background(), config, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		return nil, wantErr
	})

	// Then
	require.ErrorIs(t, err, wantErr)
}

func TestRuntime_flushesAndClosesConfiguredExporterOnShutdown(t *testing.T) {
	// Given
	exporter := &memoryExporter{}
	config := Config{Endpoint: "https://collector.example.test:4317", Protocol: grpcProtocol}
	runtime, err := newRuntime(context.Background(), config, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		return exporter, nil
	})
	require.NoError(t, err)

	// When
	_, span := runtime.TracerProvider().Tracer("telemetry-test").Start(context.Background(), "test.span")
	span.End()
	err = runtime.Shutdown(context.Background())

	// Then
	require.NoError(t, err)
	require.Len(t, exporter.exportedSpans(), 1)
	require.Equal(t, 1, exporter.shutdownCount())
}

func TestRuntime_usesGoatwayAsDefaultServiceName(t *testing.T) {
	// Given
	exporter := &memoryExporter{}
	runtime := newRuntimeWithMemoryExporter(t, exporter)

	// When
	_, span := runtime.TracerProvider().Tracer("telemetry-test").Start(context.Background(), "test.span")
	span.End()
	require.NoError(t, runtime.Shutdown(context.Background()))

	// Then
	require.Equal(t, "goatway", resourceAttribute(t, exporter.exportedSpans()[0], "service.name"))
}

func TestRuntime_usesStandardEnvironmentResourceOverrides(t *testing.T) {
	// Given
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=from-resource,service.namespace=integration")
	t.Setenv("OTEL_SERVICE_NAME", "from-service-name")
	exporter := &memoryExporter{}
	runtime := newRuntimeWithMemoryExporter(t, exporter)

	// When
	_, span := runtime.TracerProvider().Tracer("telemetry-test").Start(context.Background(), "test.span")
	span.End()
	require.NoError(t, runtime.Shutdown(context.Background()))

	// Then
	exported := exporter.exportedSpans()[0]
	require.Equal(t, "from-service-name", resourceAttribute(t, exported, "service.name"))
	require.Equal(t, "integration", resourceAttribute(t, exported, "service.namespace"))
}

func TestRuntime_HTTPHandler_extractsTraceContextWithStableOperationName(t *testing.T) {
	// Given
	exporter := &memoryExporter{}
	runtime := newRuntimeWithMemoryExporter(t, exporter)
	var traceID string
	handler := runtime.HTTPHandler(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		traceID = TraceID(request.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "http://gateway.example.test/", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	// When
	handler.ServeHTTP(httptest.NewRecorder(), request)
	require.NoError(t, runtime.Shutdown(context.Background()))

	// Then
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", traceID)
	require.Equal(t, "goatway.request", exporter.exportedSpans()[0].Name())
}

func TestRuntime_TraceContext_extractsW3CTraceParent(t *testing.T) {
	// Given
	runtime, err := New(context.Background(), Config{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })
	header := http.Header{"Traceparent": []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}

	// When
	ctx := runtime.TraceContext().Extract(context.Background(), propagation.HeaderCarrier(header))

	// Then
	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", TraceID(ctx))
}

func TestRuntime_TraceID_returnsEmptyStringForInvalidContext(t *testing.T) {
	// Given
	ctx := context.Background()

	// When
	traceID := TraceID(ctx)

	// Then
	require.Empty(t, traceID)
}

func newRuntimeWithMemoryExporter(t *testing.T, exporter *memoryExporter) *Runtime {
	t.Helper()
	config := Config{Endpoint: "https://collector.example.test:4317", Protocol: grpcProtocol}
	runtime, err := newRuntime(context.Background(), config, func(context.Context, Config) (sdktrace.SpanExporter, error) {
		return exporter, nil
	})
	require.NoError(t, err)
	return runtime
}

func resourceAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	value, ok := span.Resource().Set().Value(attribute.Key(key))
	require.True(t, ok)
	return value.AsString()
}

type memoryExporter struct {
	mu            sync.Mutex
	spans         []sdktrace.ReadOnlySpan
	shutdownCalls int
}

func (exporter *memoryExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	exporter.spans = append(exporter.spans, spans...)
	return nil
}

func (exporter *memoryExporter) Shutdown(context.Context) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	exporter.shutdownCalls++
	return nil
}

func (exporter *memoryExporter) exportedSpans() []sdktrace.ReadOnlySpan {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), exporter.spans...)
}

func (exporter *memoryExporter) shutdownCount() int {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return exporter.shutdownCalls
}
