package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/headers"
)

func TestHandler_continues_sampled_traceparent_and_ignores_client_trace_id(t *testing.T) {
	// Given
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentSpanID = "00f067aa0ba902b7"
	upstreamHeaders := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false)
	request := gatewayRequest()
	request.Header.Set(headers.TraceID, "client-spoof")
	request.Header.Set("traceparent", "00-"+traceID+"-"+parentSpanID+"-01")
	request.Header.Set("tracestate", "vendor=client")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	propagatedHeaders := <-upstreamHeaders
	propagatedContext := propagation.TraceContext{}.Extract(context.Background(), propagation.HeaderCarrier(propagatedHeaders))
	propagatedSpanContext := trace.SpanContextFromContext(propagatedContext)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
	require.True(t, propagatedSpanContext.IsValid())
	require.Equal(t, traceID, propagatedSpanContext.TraceID().String())
	require.NotEqual(t, parentSpanID, propagatedSpanContext.SpanID().String())
	require.True(t, propagatedSpanContext.TraceFlags().IsSampled())
	require.Equal(t, "vendor=client", propagatedHeaders.Get("tracestate"))
}

func TestHandler_continues_unsampled_traceparent(t *testing.T) {
	// Given
	const traceID = "5bf92f3577b34da6a3ce929d0e0e4736"
	upstreamHeaders := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false)
	request := gatewayRequest()
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-00")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	propagatedContext := propagation.TraceContext{}.Extract(context.Background(), propagation.HeaderCarrier(<-upstreamHeaders))
	propagatedSpanContext := trace.SpanContextFromContext(propagatedContext)
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
	require.Equal(t, traceID, propagatedSpanContext.TraceID().String())
	require.False(t, propagatedSpanContext.TraceFlags().IsSampled())
}

func TestHandler_creates_trace_for_invalid_traceparent(t *testing.T) {
	// Given
	upstreamHeaders := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false)
	request := gatewayRequest()
	request.Header.Set(headers.TraceID, "client-spoof")
	request.Header.Set("traceparent", "00-00000000000000000000000000000000-0000000000000000-01")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	propagatedContext := propagation.TraceContext{}.Extract(context.Background(), propagation.HeaderCarrier(<-upstreamHeaders))
	propagatedSpanContext := trace.SpanContextFromContext(propagatedContext)
	require.True(t, propagatedSpanContext.IsValid())
	require.NotEqual(t, "client-spoof", recorder.Header().Get(headers.TraceID))
	require.Equal(t, propagatedSpanContext.TraceID().String(), recorder.Header().Get(headers.TraceID))
}
