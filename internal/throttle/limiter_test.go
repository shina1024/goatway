package throttle

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_IsOverLimit_returns_article_threshold_decisions(t *testing.T) {
	tests := []struct {
		name           string
		limits         string
		client         string
		count          int
		deploymentType string
		instanceCounts InstanceCounts
		trafficWeight  TrafficWeight
		want           bool
	}{
		{
			name:           "allows count at primary-only threshold",
			limits:         "premium: 300\n",
			client:         "premium",
			count:          15,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 20},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "rejects count above primary-only threshold",
			limits:         "premium: 300\n",
			client:         "premium",
			count:          16,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 20},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           true,
		},
		{
			name:           "allows primary count at weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          10,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
		{
			name:           "rejects primary count above weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          11,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           true,
		},
		{
			name:           "allows canary count at weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          10,
			deploymentType: "canary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
		{
			name:           "rejects canary count above weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          11,
			deploymentType: "canary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           true,
		},
		{
			name:           "promotes zero weighted threshold to one",
			limits:         "premium: 1\n",
			client:         "premium",
			count:          2,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 100, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 1, Canary: 99},
			want:           true,
		},
		{
			name:           "allows unknown client",
			limits:         "premium: 100\n",
			client:         "unknown",
			count:          1000,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 1},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "allows empty deployment type while degraded",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			deploymentType: "",
			instanceCounts: InstanceCounts{Primary: 1},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "allows when no instances are available",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			deploymentType: "primary",
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "allows when no traffic weights are available",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 1},
			want:           false,
		},
		{
			name:           "allows canary when no canary pods exist",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			deploymentType: "canary",
			instanceCounts: InstanceCounts{Primary: 9},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
		{
			name:           "does not overflow with large maximum and weight",
			limits:         "premium: 2147483647\n",
			client:         "premium",
			count:          21474837,
			deploymentType: "primary",
			instanceCounts: InstanceCounts{Primary: 1, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 100, Canary: 0},
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			path := writeTestFile(t, "max_concurrent_requests.yml", tt.limits)
			limiter, err := NewLimiter(path)
			require.NoError(t, err)

			// When
			got := limiter.IsOverLimit(tt.client, tt.count, tt.deploymentType, tt.instanceCounts, tt.trafficWeight)

			// Then
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_IsOverLimit_rejects_degraded_state_when_fail_closed(t *testing.T) {
	// Given
	limiter := NewLimiterFromLimits(map[string]int{"premium": 100}, WithFailPolicy(FailClosed))

	// When
	got := limiter.IsOverLimit("premium", 1, "", InstanceCounts{}, TrafficWeight{})

	// Then
	require.True(t, got, "fail_closed degraded policy must reject configured clients")
}

func Test_IsOverLimit_defaults_to_fail_open_for_degraded_state(t *testing.T) {
	// Given
	limiter := NewLimiterFromLimits(map[string]int{"premium": 100})

	// When
	got := limiter.IsOverLimit("premium", 1, "", InstanceCounts{}, TrafficWeight{})

	// Then
	require.False(t, got)
}

func Test_IsOverLimit_fail_closed_rejects_every_degraded_state(t *testing.T) {
	tests := []struct {
		name           string
		deploymentType string
		instanceCounts InstanceCounts
		trafficWeight  TrafficWeight
	}{
		{name: "missing deployment type", instanceCounts: InstanceCounts{Primary: 1}, trafficWeight: TrafficWeight{Primary: 100}},
		{name: "zero total pods", deploymentType: "primary", trafficWeight: TrafficWeight{Primary: 100}},
		{name: "zero total weights", deploymentType: "primary", instanceCounts: InstanceCounts{Primary: 1}},
		{name: "selected primary has zero pods", deploymentType: "primary", instanceCounts: InstanceCounts{Canary: 1}, trafficWeight: TrafficWeight{Primary: 100}},
		{name: "selected canary has zero pods", deploymentType: "canary", instanceCounts: InstanceCounts{Primary: 1}, trafficWeight: TrafficWeight{Primary: 90, Canary: 10}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			limiter := NewLimiterFromLimits(map[string]int{"premium": 100}, WithFailPolicy(FailClosed))

			// When
			got := limiter.IsOverLimit("premium", 1, test.deploymentType, test.instanceCounts, test.trafficWeight)

			// Then
			require.True(t, got)
		})
	}
}

func Test_IsOverLimit_fail_closed_preserves_healthy_threshold_decisions(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  bool
	}{
		{name: "under limit", count: 100, want: false},
		{name: "over limit", count: 101, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			limiter := NewLimiterFromLimits(map[string]int{"premium": 100}, WithFailPolicy(FailClosed))

			// When
			got := limiter.IsOverLimit("premium", test.count, "primary", InstanceCounts{Primary: 1}, TrafficWeight{Primary: 100})

			// Then
			require.Equal(t, test.want, got)
		})
	}
}

func Test_IsOverLimit_does_not_apply_fail_closed_without_configured_limit(t *testing.T) {
	// Given
	limiter := NewLimiterFromLimits(map[string]int{"premium": 100}, WithFailPolicy(FailClosed))

	// When
	got := limiter.IsOverLimit("unknown", 1000, "", InstanceCounts{}, TrafficWeight{})

	// Then
	require.False(t, got)
}

func Test_Limiter_counts_active_client_requests_without_leaking(t *testing.T) {
	// Given
	limiter, err := NewLimiter(writeTestFile(t, "max_concurrent_requests.yml", "premium: 100\n"))
	require.NoError(t, err)

	// When
	require.Equal(t, 1, limiter.Inc("premium"))
	require.Equal(t, 2, limiter.Inc("premium"))
	limiter.Dec("premium")
	limiter.Dec("premium")

	// Then
	require.Equal(t, 1, limiter.Inc("premium"))
	limiter.Dec("premium")
}

func Test_Limiter_handles_concurrent_increment_decrement_without_leaking(t *testing.T) {
	// Given
	limiter, err := NewLimiter(writeTestFile(t, "max_concurrent_requests.yml", "premium: 100\n"))
	require.NoError(t, err)
	const workers = 64
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)

	// When
	for range workers {
		go func() {
			defer waitGroup.Done()
			limiter.Inc("premium")
			limiter.Dec("premium")
		}()
	}
	waitGroup.Wait()

	// Then
	require.Equal(t, 1, limiter.Inc("premium"))
	limiter.Dec("premium")
}
