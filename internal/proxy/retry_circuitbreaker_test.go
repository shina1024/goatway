package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/circuitbreaker"
	"goatway/internal/config"
)

func TestHandler_ForwardWithRetry_skips_scheduled_attempts_after_group_breaker_opens(t *testing.T) {
	// Given
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:     []config.TargetConfig{retryTarget(t, backend.URL, time.Second)},
			MaxTryCount: 2,
			RetryCases:  []string{"server_error"},
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	breakers := circuitbreaker.NewRegistry([]string{"api"}, circuitbreaker.Config{FailureThreshold: 1, OpenInterval: time.Hour, HalfOpenMaxRequests: 1})
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(
		WithRetrySleeper(func(time.Duration) {}),
		WithCircuitBreakers(breakers),
	).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, result.StatusCode)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, int64(1), calls.Load())
}

func TestHandler_ForwardWithRetry_returns_service_unavailable_when_all_groups_are_open(t *testing.T) {
	// Given
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: []config.TargetConfig{retryTarget(t, backend.URL, time.Second)}},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	breakers := circuitbreaker.NewRegistry([]string{"api"}, circuitbreaker.Config{FailureThreshold: 1, OpenInterval: time.Hour, HalfOpenMaxRequests: 1})
	breakers.Breaker("api").RecordFailure()
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithCircuitBreakers(breakers)).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, result.StatusCode)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Zero(t, calls.Load())
}

func TestHandler_ForwardWithRetry_opens_breaker_on_non_timeout_transport_error(t *testing.T) {
	// Given
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: []config.TargetConfig{retryTarget(t, "http://127.0.0.1:1", time.Second)}},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	breakers := circuitbreaker.NewRegistry([]string{"api"}, circuitbreaker.Config{FailureThreshold: 1, OpenInterval: time.Hour, HalfOpenMaxRequests: 1})
	handler := NewHandler(WithCircuitBreakers(breakers))

	// When
	firstResult, firstErr := handler.ForwardWithRetry(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)
	secondRecorder := httptest.NewRecorder()
	secondResult, secondErr := handler.ForwardWithRetry(
		secondRecorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.Error(t, firstErr)
	require.Equal(t, ErrClassOther, firstResult.ErrClass)
	require.Equal(t, http.StatusBadGateway, firstResult.StatusCode)
	require.NoError(t, secondErr)
	require.Equal(t, http.StatusServiceUnavailable, secondResult.StatusCode)
	require.Equal(t, http.StatusServiceUnavailable, secondRecorder.Code)
}

func TestHandler_ForwardWithRetry_does_not_open_breaker_on_response_too_large(t *testing.T) {
	// Given
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = writer.Write([]byte("too large"))
			return
		}
		_, _ = writer.Write([]byte("x"))
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: []config.TargetConfig{retryTarget(t, backend.URL, time.Second)}},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	breakers := circuitbreaker.NewRegistry([]string{"api"}, circuitbreaker.Config{FailureThreshold: 1, OpenInterval: time.Hour, HalfOpenMaxRequests: 1})
	handler := NewHandler(WithMaxResponseBodySize(1), WithCircuitBreakers(breakers))

	// When
	firstResult, firstErr := handler.ForwardWithRetry(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)
	secondRecorder := httptest.NewRecorder()
	secondResult, secondErr := handler.ForwardWithRetry(
		secondRecorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.Error(t, firstErr)
	require.ErrorIs(t, firstErr, ErrResponseTooLarge)
	require.Equal(t, http.StatusBadGateway, firstResult.StatusCode)
	require.NoError(t, secondErr)
	require.Equal(t, http.StatusOK, secondResult.StatusCode)
	require.Equal(t, http.StatusOK, secondRecorder.Code)
	require.Equal(t, int64(2), calls.Load())
}
