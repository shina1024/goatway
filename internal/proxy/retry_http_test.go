package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
)

func TestHandler_ForwardWithRetry_returns_success_from_next_target_when_server_error(t *testing.T) {
	// Given
	var firstPath, secondPath atomic.Pointer[string]
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		firstPath.Store(&path)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		secondPath.Store(&path)
		writer.WriteHeader(http.StatusOK)
	}))
	defer second.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:           []config.TargetConfig{retryTarget(t, first.URL, time.Second), retryTarget(t, second.URL, time.Second)},
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
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "/rewritten", *firstPath.Load())
	require.Equal(t, "/rewritten", *secondPath.Load())
}

func TestHandler_ForwardWithRetry_returns_first_server_error_when_post_retries_are_disabled(t *testing.T) {
	// Given
	var firstCalls, secondCalls atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer second.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:            []config.TargetConfig{retryTarget(t, first.URL, time.Second), retryTarget(t, second.URL, time.Second)},
			MaxTryCount:        2,
			RetryCases:         []string{"server_error"},
			RetryNonIdempotent: false,
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodPost, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, result.StatusCode)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, int64(1), firstCalls.Load())
	require.Zero(t, secondCalls.Load())
}

func TestHandler_ForwardWithRetry_does_not_retry_timeout_when_only_server_errors_are_enabled(t *testing.T) {
	// Given
	var firstCalls, secondCalls atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		firstCalls.Add(1)
		<-request.Context().Done()
	}))
	defer slow.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:     []config.TargetConfig{retryTarget(t, slow.URL, 20*time.Millisecond), retryTarget(t, healthy.URL, time.Second)},
			MaxTryCount: 2,
			RetryCases:  []string{"server_error"},
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.Error(t, err)
	require.Equal(t, ErrClassTimeout, result.ErrClass)
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	require.Equal(t, int64(1), firstCalls.Load())
	require.Zero(t, secondCalls.Load())
}

func TestHandler_ForwardWithRetry_retries_timeout_against_healthy_target(t *testing.T) {
	// Given
	var firstCalls, secondCalls atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		firstCalls.Add(1)
		<-request.Context().Done()
	}))
	defer slow.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:     []config.TargetConfig{retryTarget(t, slow.URL, 20*time.Millisecond), retryTarget(t, healthy.URL, time.Second)},
			MaxTryCount: 2,
			RetryCases:  []string{"timeout"},
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), firstCalls.Load())
	require.Equal(t, int64(1), secondCalls.Load())
}
