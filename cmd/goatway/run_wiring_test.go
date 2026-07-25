package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/headers"
)

func Test_run_wires_explicit_telemetry_through_gateway(t *testing.T) {
	// Given
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	runtime := &instrumentedRuntime{provider: provider}
	upstreamTraceIDs := make(chan trace.TraceID, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		propagator := propagation.TraceContext{}
		extracted := propagator.Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		upstreamTraceIDs <- trace.SpanContextFromContext(extracted).TraceID()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	requestResults := make(chan error, 1)
	server := testServer{
		listen: func() error {
			request := httptest.NewRequest(http.MethodGet, "/items/42", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set(headers.APIToken, "token")
			response := httptest.NewRecorder()
			runtime.handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				requestResults <- fmt.Errorf("gateway response status = %d", response.Code)
				return http.ErrServerClosed
			}
			requestResults <- nil
			return http.ErrServerClosed
		},
		shutdown: func(context.Context) error { return nil },
	}

	// When
	dependencies := testDependencies(runtime, server)
	err := run(context.Background(), testSettings(testConfigDir(t, upstream.URL)), dependencies)

	// Then
	require.NoError(t, err)
	require.NoError(t, <-requestResults)
	var serverSpan sdktrace.ReadOnlySpan
	var clientSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.SpanKind() {
		case trace.SpanKindServer:
			if span.Name() == "goatway.request" {
				serverSpan = span
			}
		case trace.SpanKindClient:
			clientSpan = span
		}
	}
	require.NotNil(t, serverSpan)
	require.NotNil(t, clientSpan)
	upstreamTraceID := <-upstreamTraceIDs
	require.True(t, upstreamTraceID.IsValid())
	require.Equal(t, serverSpan.SpanContext().TraceID(), clientSpan.SpanContext().TraceID())
	require.Equal(t, serverSpan.SpanContext().TraceID(), upstreamTraceID)
	require.Equal(t, 1, runtime.handlerCalls)
	require.Equal(t, 1, runtime.shutdownCalls)
}

type instrumentedRuntime struct {
	provider      *sdktrace.TracerProvider
	handler       http.Handler
	handlerCalls  int
	shutdownCalls int
}

func (runtime *instrumentedRuntime) TracerProvider() trace.TracerProvider {
	return runtime.provider
}

func (runtime *instrumentedRuntime) TraceContext() propagation.TraceContext {
	return propagation.TraceContext{}
}

func (runtime *instrumentedRuntime) HTTPHandler(next http.Handler) http.Handler {
	runtime.handlerCalls++
	runtime.handler = otelhttp.NewHandler(
		next,
		"goatway.request",
		otelhttp.WithTracerProvider(runtime.provider),
		otelhttp.WithPropagators(runtime.TraceContext()),
		otelhttp.WithSpanNameFormatter(func(operation string, _ *http.Request) string {
			return operation
		}),
	)
	return runtime.handler
}

func (runtime *instrumentedRuntime) Shutdown(ctx context.Context) error {
	runtime.shutdownCalls++
	return runtime.provider.Shutdown(ctx)
}
