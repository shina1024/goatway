package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/config"
	"goatway/internal/router"
)

const transferSpanName = "goatway.proxy.transfer"

func newRetryTelemetryHandler(t *testing.T, options ...Option) (*Handler, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	return NewHandler(append([]Option{WithTelemetry(provider, propagation.TraceContext{})}, options...)...), recorder
}

func transferSpan(t *testing.T, recorder *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	var spans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == transferSpanName {
			spans = append(spans, span)
		}
	}
	require.Len(t, spans, 1)
	return spans[0]
}

func spanAttribute(t *testing.T, span sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()
	for _, value := range span.Attributes() {
		if string(value.Key) == key {
			return value.Value
		}
	}
	t.Fatalf("span %q is missing attribute %q", span.Name(), key)
	return attribute.Value{}
}

func clientSpans(recorder *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	var spans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindClient {
			spans = append(spans, span)
		}
	}
	return spans
}

func TestHandler_ForwardWithRetry_creates_one_transfer_span_with_a_client_child_when_successful(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, _ := testTarget(t, backend.URL, time.Second)
	handler, proxyRecorder := newRetryTelemetryHandler(t)
	serverRecorder := tracetest.NewSpanRecorder()
	serverProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(serverRecorder))
	t.Cleanup(func() { require.NoError(t, serverProvider.Shutdown(context.Background())) })
	outcomes := make(chan error, 1)
	inbound := otelhttp.NewHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, err := handler.ForwardWithRetry(writer, request, RetryInput{Group: group, Match: router.Match{
			ClientType:    "test-client",
			RoutedPathMap: map[string]string{string(group.ID()): "/"},
		}})
		outcomes <- err
	}), "inbound", otelhttp.WithTracerProvider(serverProvider))
	server := httptest.NewServer(inbound)
	defer server.Close()

	// When
	response, err := http.Get(server.URL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.NoError(t, <-outcomes)

	// Then
	require.Equal(t, http.StatusOK, response.StatusCode)
	transfer := transferSpan(t, proxyRecorder)
	clients := clientSpans(proxyRecorder)
	require.Len(t, clients, 1)
	var inboundSpan sdktrace.ReadOnlySpan
	for _, span := range serverRecorder.Ended() {
		if span.SpanKind() == trace.SpanKindServer {
			inboundSpan = span
		}
	}
	require.NotNil(t, inboundSpan)
	require.Equal(t, inboundSpan.SpanContext().TraceID(), transfer.SpanContext().TraceID())
	require.Equal(t, inboundSpan.SpanContext().SpanID(), transfer.Parent().SpanID())
	require.Equal(t, transfer.SpanContext().SpanID(), clients[0].Parent().SpanID())
	require.Equal(t, "api", spanAttribute(t, transfer, "goatway.proxy.target_group.id").AsString())
	require.Equal(t, "test-client", spanAttribute(t, transfer, "goatway.proxy.client.type").AsString())
	require.EqualValues(t, 1, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.EqualValues(t, http.StatusOK, spanAttribute(t, transfer, "http.response.status_code").AsInt64())
	require.Equal(t, codes.Unset, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_makes_retry_client_spans_siblings_beneath_transfer(t *testing.T) {
	// Given
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{"api": {
		Targets:     []config.TargetConfig{retryTarget(t, backend.URL, time.Second), retryTarget(t, backend.URL, time.Second)},
		MaxTryCount: 2,
		RetryCases:  []string{"server_error"},
	}})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	handler, recorder := newRetryTelemetryHandler(t, WithRetrySleeper(func(time.Duration) {}))

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, int64(2), calls.Load())
	transfer := transferSpan(t, recorder)
	clients := clientSpans(recorder)
	require.Len(t, clients, 2)
	for _, client := range clients {
		require.Equal(t, transfer.SpanContext().SpanID(), client.Parent().SpanID())
	}
	require.EqualValues(t, 2, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.Equal(t, codes.Unset, transfer.Status().Code)
}
