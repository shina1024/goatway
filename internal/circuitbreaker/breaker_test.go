package circuitbreaker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBreaker_denies_requests_after_consecutive_failures_until_open_interval_elapses(t *testing.T) {
	// Given
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := newBreaker(Config{FailureThreshold: 2, OpenInterval: time.Minute, HalfOpenMaxRequests: 1}, func() time.Time { return now })

	// When
	breaker.RecordFailure()
	breaker.RecordFailure()

	// Then
	require.False(t, breaker.Allow())
	now = now.Add(time.Minute)
	require.True(t, breaker.Allow())
}

func TestBreaker_closes_when_half_open_probe_succeeds(t *testing.T) {
	// Given
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := newBreaker(Config{FailureThreshold: 1, OpenInterval: time.Minute, HalfOpenMaxRequests: 1}, func() time.Time { return now })
	breaker.RecordFailure()
	now = now.Add(time.Minute)
	require.True(t, breaker.Allow())

	// When
	breaker.RecordSuccess()

	// Then
	require.True(t, breaker.Allow())
}

func TestBreaker_reopens_when_half_open_probe_fails(t *testing.T) {
	// Given
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	breaker := newBreaker(Config{FailureThreshold: 1, OpenInterval: time.Minute, HalfOpenMaxRequests: 1}, func() time.Time { return now })
	breaker.RecordFailure()
	now = now.Add(time.Minute)
	require.True(t, breaker.Allow())

	// When
	breaker.RecordFailure()

	// Then
	require.False(t, breaker.Allow())
}

func TestRegistry_returns_breakers_only_for_configured_target_groups(t *testing.T) {
	// Given
	registry := NewRegistry([]string{"api"}, Config{FailureThreshold: 1, OpenInterval: time.Minute, HalfOpenMaxRequests: 1})

	// When
	breaker := registry.Breaker("api")

	// Then
	require.NotNil(t, breaker)
	require.Nil(t, registry.Breaker("absent"))
}
