package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"

	"goatway/internal/config"
)

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("body read failure") }

func (failingReadCloser) Close() error { return nil }

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_group_is_nil(t *testing.T) {
	// Given
	handler, recorder := newRetryTelemetryHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	// When
	_, err := handler.ForwardWithRetry(httptest.NewRecorder(), request, RetryInput{})

	// Then
	require.ErrorIs(t, err, ErrNilTargetGroup)
	transfer := transferSpan(t, recorder)
	require.EqualValues(t, 0, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_body_buffering_fails(t *testing.T) {
	// Given
	group, _ := testTarget(t, "http://127.0.0.1:8080", time.Second)
	handler, recorder := newRetryTelemetryHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/", failingReadCloser{})

	// When
	_, err := handler.ForwardWithRetry(httptest.NewRecorder(), request, retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.Error(t, err)
	transfer := transferSpan(t, recorder)
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_schedule_is_empty(t *testing.T) {
	// Given
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{"api": {
		Targets:     []config.TargetConfig{retryTarget(t, "http://127.0.0.1:8080", time.Second)},
		MaxTryCount: -1,
	}})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	handler, recorder := newRetryTelemetryHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	// When
	_, err = handler.ForwardWithRetry(httptest.NewRecorder(), request, retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.Error(t, err)
	transfer := transferSpan(t, recorder)
	require.EqualValues(t, 0, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

func TestHandler_ForwardWithRetry_ends_transfer_with_error_when_retry_wait_is_cancelled(t *testing.T) {
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
	handler, recorder := newRetryTelemetryHandler(t, withRetryWaiter(func(context.Context, time.Duration) error { return context.Canceled }))

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, clientClosedStatus, result.StatusCode)
	transfer := transferSpan(t, recorder)
	require.EqualValues(t, 1, spanAttribute(t, transfer, "goatway.proxy.attempt_count").AsInt64())
	require.Equal(t, "other", spanAttribute(t, transfer, "error.type").AsString())
	require.Equal(t, codes.Error, transfer.Status().Code)
}

var _ io.ReadCloser = failingReadCloser{}
