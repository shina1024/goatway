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
