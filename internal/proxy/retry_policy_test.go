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

func TestHandler_ForwardWithRetry_uses_retry_group_rewrite_path_after_cross_group_failure(t *testing.T) {
	// Given
	var primaryPath, fallbackPath atomic.Pointer[string]
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		primaryPath.Store(&path)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		fallbackPath.Store(&path)
		writer.WriteHeader(http.StatusOK)
	}))
	defer fallback.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"primary": {
			Targets:              []config.TargetConfig{retryTarget(t, primary.URL, time.Second)},
			MaxTryCount:          2,
			RetryCases:           []string{"server_error"},
			RetryToTargetGroupID: "fallback",
		},
		"fallback": {Targets: []config.TargetConfig{retryTarget(t, fallback.URL, time.Second)}},
	})
	group, err := registry.Lookup("primary")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"primary": "/primary-path", "fallback": "/fallback-path"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "/primary-path", *primaryPath.Load())
	require.Equal(t, "/fallback-path", *fallbackPath.Load())
}

func TestHandler_ForwardWithRetry_returns_last_server_error_when_retries_are_exhausted(t *testing.T) {
	// Given
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
		_, err := writer.Write([]byte("last failure"))
		require.NoError(t, err)
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
	recorder := httptest.NewRecorder()

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, result.StatusCode)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, "last failure", recorder.Body.String())
	require.Equal(t, int64(2), calls.Load())
}

func TestHandler_ForwardWithRetry_returns_gateway_timeout_when_timeout_retries_are_exhausted(t *testing.T) {
	// Given
	var calls atomic.Int64
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		<-request.Context().Done()
	}))
	defer slow.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:     []config.TargetConfig{retryTarget(t, slow.URL, 20*time.Millisecond)},
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
	require.Error(t, err)
	require.Equal(t, ErrClassTimeout, result.ErrClass)
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	require.Equal(t, http.StatusGatewayTimeout, result.StatusCode)
	require.Equal(t, int64(2), calls.Load())
}

func TestHandler_ForwardWithRetry_returns_bad_gateway_when_transport_error_is_not_timeout(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	backendURL := backend.URL
	backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:     []config.TargetConfig{retryTarget(t, backendURL, time.Second)},
			MaxTryCount: 1,
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
	require.Equal(t, ErrClassOther, result.ErrClass)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, http.StatusBadGateway, result.StatusCode)
}

func TestHandler_ForwardWithRetry_limits_attempts_to_max_try_count(t *testing.T) {
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
			MaxTryCount: 3,
			RetryCases:  []string{"server_error"},
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	// When
	_, err = NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		recorder,
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Equal(t, int64(3), calls.Load())
}
