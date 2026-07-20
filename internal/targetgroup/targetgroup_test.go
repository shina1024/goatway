package targetgroup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
)

func TestNewRegistry_resolves_target_timeouts_by_precedence(t *testing.T) {
	tests := []struct {
		name          string
		targetTimeout config.Milliseconds
		groupTimeout  config.Milliseconds
		wantConnect   time.Duration
		wantRead      time.Duration
		wantIdle      time.Duration
	}{
		{name: "uses target timeout", targetTimeout: 17, groupTimeout: 31, wantConnect: 17 * time.Millisecond, wantRead: 17 * time.Millisecond, wantIdle: 17 * time.Millisecond},
		{name: "uses group timeout", groupTimeout: 31, wantConnect: 31 * time.Millisecond, wantRead: 31 * time.Millisecond, wantIdle: 31 * time.Millisecond},
		{name: "uses default timeout", wantConnect: time.Second, wantRead: 10 * time.Second, wantIdle: 90 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			groupConfig := config.TargetGroupConfig{
				ConnectTimeout:  test.groupTimeout,
				ReadTimeout:     test.groupTimeout,
				IdleConnTimeout: test.groupTimeout,
				Targets: []config.TargetConfig{{
					Host:            "api.example.test",
					Port:            8443,
					Weight:          1,
					ConnectTimeout:  test.targetTimeout,
					ReadTimeout:     test.targetTimeout,
					IdleConnTimeout: test.targetTimeout,
				}},
			}
			registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{"api": groupConfig})

			// When
			group, err := registry.Lookup("api")

			// Then
			require.NoError(t, err)
			target := group.Targets()[0]
			require.Equal(t, test.wantConnect, target.ConnectTimeout())
			require.Equal(t, test.wantRead, target.ReadTimeout())
			require.Equal(t, test.wantIdle, target.IdleConnTimeout())
			require.Equal(t, "api.example.test:8443", target.Address())
		})
	}
}

func TestTargetGroup_ScheduledTargets_rotates_equal_weight_first_target(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: targets("a", "b", "c")},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)

	// When
	first := group.ScheduledTargets(1)
	second := group.ScheduledTargets(1)
	third := group.ScheduledTargets(1)

	// Then
	require.Equal(t, []string{"a:8080"}, addresses(first))
	require.Equal(t, []string{"b:8080"}, addresses(second))
	require.Equal(t, []string{"c:8080"}, addresses(third))
}

func TestTargetGroup_ScheduledTargets_follows_default_retry_map_with_wrap(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: targets("a", "b", "c")},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)

	// When
	got := group.ScheduledTargets(4)

	// Then
	require.Equal(t, []string{"a:8080", "b:8080", "c:8080", "a:8080"}, addresses(got))
}

func TestTargetGroup_ScheduledTargets_honors_explicit_retry_map(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: []config.TargetConfig{
			{Host: "a1", Port: 8080, Weight: 1, RetryTo: "b2:8080"},
			{Host: "a2", Port: 8080, Weight: 1, RetryTo: "a1:8080"},
			{Host: "b1", Port: 8080, Weight: 1, RetryTo: "a2:8080"},
			{Host: "b2", Port: 8080, Weight: 1, RetryTo: "b1:8080"},
		}},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)

	// When
	got := group.ScheduledTargets(4)

	// Then
	require.Equal(t, []string{"a1:8080", "b2:8080", "b1:8080", "a2:8080"}, addresses(got))
}

func TestNewRegistry_applies_retry_defaults(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: targets("a", "b", "c"), RetryCases: []string{"server_error", "timeout"}},
	})

	// When
	group, err := registry.Lookup("api")

	// Then
	require.NoError(t, err)
	require.Equal(t, 3, group.MaxTryCount())
	require.Equal(t, []RetryCase{RetryCaseServerError, RetryCaseTimeout}, group.RetryCases())
	require.Equal(t, 50*time.Millisecond, group.RetryBaseInterval())
	require.Equal(t, 500*time.Millisecond, group.RetryMaxInterval())
}

func TestNewRegistry_wires_retry_target_group(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"primary":  {Targets: targets("primary", "primary-alt"), RetryToTargetGroupID: "fallback"},
		"fallback": {Targets: targets("fallback", "fallback-alt")},
	})

	// When
	primary, err := registry.Lookup("primary")

	// Then
	require.NoError(t, err)
	require.NotNil(t, primary.RetryToTargetGroup())
	require.Equal(t, config.TargetGroupID("fallback"), primary.RetryToTargetGroup().ID())
}

func TestRegistry_Lookup_returns_typed_error_when_target_group_is_unknown(t *testing.T) {
	// Given
	registry := newRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {Targets: targets("a", "b")},
	})

	// When
	_, err := registry.Lookup("missing")

	// Then
	var notFound *TargetGroupNotFoundError
	require.ErrorAs(t, err, &notFound)
	require.Equal(t, config.TargetGroupID("missing"), notFound.ID)
}

func newRegistry(t *testing.T, configs map[config.TargetGroupID]config.TargetGroupConfig) *Registry {
	t.Helper()
	registry, err := NewRegistry(configs)
	require.NoError(t, err)
	return registry
}

func targets(hosts ...string) []config.TargetConfig {
	result := make([]config.TargetConfig, len(hosts))
	for index, host := range hosts {
		result[index] = config.TargetConfig{Host: host, Port: 8080, Weight: 1}
	}
	return result
}

func addresses(targets []Target) []string {
	result := make([]string, len(targets))
	for index, target := range targets {
		result[index] = target.Address()
	}
	return result
}
