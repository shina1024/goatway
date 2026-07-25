package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"goatway/internal/config"
	"goatway/internal/headers"
	"goatway/internal/router"
)

func TestHandler_Forward_removes_client_cookies_and_upstream_set_cookies(t *testing.T) {
	// Given
	upstreamCookie := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamCookie <- request.Header.Get("Cookie")
		writer.Header().Add("Set-Cookie", "session=upstream")
		writer.Header().Add("Set-Cookie", "preference=upstream")
		writer.Header().Set("X-Upstream-Safe", "forwarded")
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Cookie", "session=client")
	recorder := httptest.NewRecorder()

	// When
	_, err := NewHandler().Forward(recorder, request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
	})

	// Then
	require.NoError(t, err)
	require.Empty(t, <-upstreamCookie)
	require.Empty(t, recorder.Result().Cookies())
	require.Equal(t, "forwarded", recorder.Header().Get("X-Upstream-Safe"))
}

func TestHandler_Forward_preserves_authoritative_response_trace_when_upstream_sends_trace_metadata(t *testing.T) {
	// Given
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	requestContext, parent := provider.Tracer("proxy-test").Start(context.Background(), "parent")
	t.Cleanup(func() { parent.End() })
	traceID := parent.SpanContext().TraceID().String()
	upstreamHeaders := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.Header().Set(headers.TraceID, "upstream-trace")
		writer.Header().Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
		writer.Header().Set("tracestate", "vendor=upstream")
		writer.Header().Set("baggage", "tenant=upstream")
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestContext)
	request.Header.Set(headers.TraceID, "client-trace")
	request.Header.Set("traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	request.Header.Set("tracestate", "vendor=client")
	request.Header.Set("baggage", "tenant=client")
	recorder := httptest.NewRecorder()
	recorder.Header().Set(headers.TraceID, traceID)

	// When
	_, err := NewHandler(WithTelemetry(provider, propagation.TraceContext{})).Forward(recorder, request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
	})

	// Then
	require.NoError(t, err)
	gotUpstream := <-upstreamHeaders
	require.Equal(t, traceID, gotUpstream.Get(headers.TraceID))
	require.NotEqual(t, "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01", gotUpstream.Get("traceparent"))
	require.Empty(t, gotUpstream.Get("tracestate"))
	require.Empty(t, gotUpstream.Get("baggage"))
	require.Equal(t, traceID, recorder.Header().Get(headers.TraceID))
	require.Empty(t, recorder.Header().Get("traceparent"))
	require.Empty(t, recorder.Header().Get("tracestate"))
	require.Empty(t, recorder.Header().Get("baggage"))
}

func TestHandler_ForwardWithRetry_removes_cookies_from_selected_response(t *testing.T) {
	// Given
	discarded := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Set-Cookie", "discarded=first")
		writer.Header().Set("X-Discarded-Attempt", "must-not-leak")
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer discarded.Close()
	selected := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Set-Cookie", "session=selected")
		writer.Header().Add("Set-Cookie", "preference=selected")
		writer.Header().Set("X-Selected-Safe", "forwarded")
		writer.WriteHeader(http.StatusOK)
	}))
	defer selected.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:           []config.TargetConfig{retryTarget(t, discarded.URL, time.Second), retryTarget(t, selected.URL, time.Second)},
			MaxTryCount:       2,
			RetryCases:        []string{"server_error"},
			RetryBaseInterval: 1,
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
		retryInput(group, map[string]string{"api": "/"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Values("Set-Cookie"))
	require.Equal(t, "forwarded", recorder.Header().Get("X-Selected-Safe"))
	require.Empty(t, recorder.Header().Get("X-Discarded-Attempt"))
}
