package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestHandler_ForwardWithRetry_stops_when_context_is_cancelled_during_backoff(t *testing.T) {
	// Given
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer first.Close()
	var fallbackCalls atomic.Int64
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer fallback.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets: []config.TargetConfig{
				retryTarget(t, first.URL, time.Second),
				retryTarget(t, fallback.URL, time.Second),
			},
			MaxTryCount: 2,
			RetryCases:  []string{"server_error"},
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	waiting := make(chan struct{})
	handler := NewHandler(withRetryWaiter(func(ctx context.Context, _ time.Duration) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/incoming", nil).WithContext(ctx)
	type outcome struct {
		result AttemptResult
		err    error
	}
	completed := make(chan outcome, 1)

	// When
	go func() {
		result, forwardErr := handler.ForwardWithRetry(
			httptest.NewRecorder(),
			request,
			retryInput(group, map[string]string{"api": "/rewritten"}),
		)
		completed <- outcome{result: result, err: forwardErr}
	}()
	<-waiting
	cancel()
	got := <-completed

	// Then
	require.ErrorIs(t, got.err, context.Canceled)
	require.Equal(t, 460, got.result.StatusCode)
	require.Zero(t, fallbackCalls.Load())
}

func TestFullJitter_returns_sub_millisecond_values(t *testing.T) {
	// Given: a cap below one millisecond
	cap := 500 * time.Microsecond

	// When: sampling many jittered values
	seenNonZero := false
	for range 100 {
		if fullJitter(cap) > 0 {
			seenNonZero = true
			break
		}
	}

	// Then: sub-millisecond caps must not always collapse to zero
	require.True(t, seenNonZero, "fullJitter(%v) returned 0 in 100 samples; sub-ms precision lost", cap)
}

func TestFullJitter_stays_within_cap(t *testing.T) {
	cap := 7*time.Millisecond + 300*time.Microsecond
	for range 200 {
		got := fullJitter(cap)
		require.GreaterOrEqual(t, got, time.Duration(0))
		require.LessOrEqual(t, got, cap)
	}
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
