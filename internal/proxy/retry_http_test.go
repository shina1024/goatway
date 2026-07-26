package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHandler_ForwardWithRetry_returns_bad_gateway_without_retry_when_response_exceeds_limit(t *testing.T) {
	// Given
	var firstCalls, secondCalls atomic.Int64
	oversizedBody := strings.Repeat("x", maxRequestBodySize+1)
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(oversizedBody))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer second.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:           []config.TargetConfig{retryTarget(t, first.URL, time.Second), retryTarget(t, second.URL, time.Second)},
			MaxTryCount:       2,
			RetryCases:        []string{"timeout", "server_error"},
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
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Error(t, err)
	require.Equal(t, ErrClassOther, result.ErrClass)
	require.Equal(t, "Bad Gateway\n", recorder.Body.String())
	require.Equal(t, int64(1), firstCalls.Load())
	require.Zero(t, secondCalls.Load())
}

func TestHandler_ForwardWithRetry_forwards_response_at_configured_limit(t *testing.T) {
	// Given
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("body"))
	}))
	defer upstream.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: []config.TargetConfig{retryTarget(t, upstream.URL, time.Second)}},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithMaxResponseBodySize(4)).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, result.StatusCode)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "body", recorder.Body.String())
}
