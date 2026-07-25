package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/router"
)

func TestHandler_ClientFor_reuses_one_instrumented_tuned_transport_when_timeouts_match(t *testing.T) {
	// Given
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	group, target := testTarget(t, "http://127.0.0.1:8080", 100*time.Millisecond)
	handler := NewHandler(WithTelemetry(provider, propagation.TraceContext{}))

	// When
	first := handler.ClientFor(target, group.MaxIdleConnsPerHost())
	second := handler.ClientFor(target, group.MaxIdleConnsPerHost())

	// Then
	require.Same(t, first, second)
	instrumented, ok := first.Transport.(*otelhttp.Transport)
	require.True(t, ok)
	require.Same(t, instrumented, second.Transport)
	key := clientKey{
		connectTimeout:      target.ConnectTimeout(),
		readTimeout:         target.ReadTimeout(),
		idleConnTimeout:     target.IdleConnTimeout(),
		maxIdleConnsPerHost: group.MaxIdleConnsPerHost(),
	}
	entry, exists := handler.clients.clients[key]
	require.True(t, exists)
	require.Same(t, first, entry.client)
	require.Equal(t, target.ReadTimeout(), first.Timeout)
	require.Equal(t, target.IdleConnTimeout(), entry.baseTransport.IdleConnTimeout)
	require.Equal(t, group.MaxIdleConnsPerHost(), entry.baseTransport.MaxIdleConnsPerHost)
	require.NotNil(t, entry.baseTransport.DialContext)
}

func TestHandler_Forward_injects_traceparent_and_records_client_span_with_explicit_telemetry(t *testing.T) {
	// Given
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	traceparent := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		traceparent <- request.Header.Get("traceparent")
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	parentContext, parent := provider.Tracer("proxy-test").Start(context.Background(), "parent")
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(parentContext)
	request.Header.Set("traceparent", "00-00000000000000000000000000000000-0000000000000000-00")
	handler := NewHandler(WithTelemetry(provider, propagation.TraceContext{}))

	// When
	result, err := handler.Forward(httptest.NewRecorder(), request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
	})
	parent.End()

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	propagated := <-traceparent
	propagatedContext := propagation.TraceContext{}.Extract(
		context.Background(),
		propagation.HeaderCarrier(http.Header{"Traceparent": []string{propagated}}),
	)
	propagatedSpanContext := trace.SpanContextFromContext(propagatedContext)
	require.True(t, propagatedSpanContext.IsValid())
	require.Equal(t, parent.SpanContext().TraceID(), propagatedSpanContext.TraceID())
	require.NotEqual(t, parent.SpanContext().SpanID(), propagatedSpanContext.SpanID())

	var clientSpans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindClient {
			clientSpans = append(clientSpans, span)
		}
	}
	require.Len(t, clientSpans, 1)
}
