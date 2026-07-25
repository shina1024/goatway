package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"goatway/internal/headers"
)

func TestHandler_sets_trace_id_before_local_route_rejection(t *testing.T) {
	// Given
	const traceID = "6bf92f3577b34da6a3ce929d0e0e4736"
	handler := newTestHandler(t, testConfig(t, "127.0.0.1", 1), "public: 1\n", false)
	request := gatewayRequest()
	request.URL.Path = "/missing"
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
}

func TestHandler_sets_trace_id_before_upstream_failure(t *testing.T) {
	// Given
	const traceID = "7bf92f3577b34da6a3ce929d0e0e4736"
	handler := newTestHandler(t, testConfig(t, "127.0.0.1", 1), "public: 1\n", false)
	request := gatewayRequest()
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
}

func TestHandler_preserves_authoritative_trace_id_when_backend_spoofs_trace_headers(t *testing.T) {
	// Given
	const traceID = "8bf92f3577b34da6a3ce929d0e0e4736"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set(headers.TraceID, "backend-spoof")
		writer.Header().Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
		writer.Header().Set("tracestate", "vendor=backend")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false)
	request := gatewayRequest()
	request.Header.Set(headers.TraceID, "client-spoof")
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
	require.Empty(t, recorder.Header().Get("traceparent"))
	require.Empty(t, recorder.Header().Get("tracestate"))
}

func TestHandler_logs_active_trace_id(t *testing.T) {
	// Given
	const traceID = "9bf92f3577b34da6a3ce929d0e0e4736"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	var logs bytes.Buffer
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(
		t,
		testConfig(t, host, port),
		"public: 1\n",
		false,
		WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)
	request := gatewayRequest()
	request.Header.Set(headers.TraceID, "client-spoof")
	request.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
	require.Contains(t, logs.String(), `"trace_id":"`+traceID+`"`)
}
