package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
)

func TestRetryBackoffCap_applies_exponential_cap_and_default_maximum(t *testing.T) {
	// Given
	base := 10 * time.Millisecond

	// When / Then
	require.Equal(t, 10*time.Millisecond, retryBackoffCap(base, 0, 0))
	require.Equal(t, 20*time.Millisecond, retryBackoffCap(base, 0, 1))
	require.Equal(t, 100*time.Millisecond, retryBackoffCap(base, 0, 9))
	require.Equal(t, 5*time.Millisecond, retryBackoffCap(base, 5*time.Millisecond, 0))
	require.Equal(t, 35*time.Millisecond, retryBackoffCap(base, 35*time.Millisecond, 9))
}

func TestHandler_ForwardWithRetry_sleeps_with_full_jitter_between_attempts(t *testing.T) {
	// Given
	delays := make([]time.Duration, 0, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:           []config.TargetConfig{retryTarget(t, backend.URL, time.Second)},
			MaxTryCount:       3,
			RetryCases:        []string{"server_error"},
			RetryBaseInterval: 10,
			RetryMaxInterval:  25,
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	handler := NewHandler(WithRetrySleeper(func(delay time.Duration) { delays = append(delays, delay) }))

	// When
	_, err = handler.ForwardWithRetry(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/incoming", nil),
		retryInput(group, map[string]string{"api": "/rewritten"}),
	)

	// Then
	require.NoError(t, err)
	require.Len(t, delays, 2)
	require.GreaterOrEqual(t, delays[0], time.Duration(0))
	require.LessOrEqual(t, delays[0], retryBackoffCap(10*time.Millisecond, 25*time.Millisecond, 1))
	require.GreaterOrEqual(t, delays[1], time.Duration(0))
	require.LessOrEqual(t, delays[1], retryBackoffCap(10*time.Millisecond, 25*time.Millisecond, 2))
}
