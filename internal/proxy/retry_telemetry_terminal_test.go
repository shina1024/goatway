package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"

	"goatway/internal/config"
)

type failingResponseWriter struct{ header http.Header }

func (writer *failingResponseWriter) Header() http.Header { return writer.header }

func (writer *failingResponseWriter) WriteHeader(int) {}

func (writer *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("writer failure")
}

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_transport_fails(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.NotFoundHandler())
	url := backend.URL
	backend.Close()
	group, _ := testTarget(t, url, time.Second)
	handler, recorder := newRetryTelemetryHandler(t)

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.Error(t, err)
	require.Equal(t, http.StatusBadGateway, result.StatusCode)
	transfer := transferSpan(t, recorder)
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_timeout_when_transport_times_out(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer backend.Close()
	group, _ := testTarget(t, backend.URL, time.Millisecond)
	handler, recorder := newRetryTelemetryHandler(t)

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.Error(t, err)
	require.Equal(t, http.StatusGatewayTimeout, result.StatusCode)
	transfer := transferSpan(t, recorder)
	require.Equal(t, "timeout", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_selected_response_write_fails(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, _ := testTarget(t, backend.URL, time.Second)
	handler, recorder := newRetryTelemetryHandler(t)
	writer := &failingResponseWriter{header: make(http.Header)}

	// When
	result, err := handler.ForwardWithRetry(writer, httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.Error(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	transfer := transferSpan(t, recorder)
	require.EqualValues(t, http.StatusOK, spanAttribute(t, transfer, "http.response.status_code").AsInt64())
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_server_error_when_retries_are_exhausted(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
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
	require.Equal(t, http.StatusInternalServerError, result.StatusCode)
	transfer := transferSpan(t, recorder)
	require.EqualValues(t, 2, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.EqualValues(t, http.StatusInternalServerError, spanAttribute(t, transfer, "http.response.status_code").AsInt64())
	require.Equal(t, "server_error", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}
