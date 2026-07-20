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
		depType        string
		instanceCounts InstanceCounts
		trafficWeight  TrafficWeight
		want           bool
	}{
		{
			name:           "allows count at primary-only threshold",
			limits:         "premium: 300\n",
			client:         "premium",
			count:          15,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 20},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "rejects count above primary-only threshold",
			limits:         "premium: 300\n",
			client:         "premium",
			count:          16,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 20},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           true,
		},
		{
			name:           "allows primary count at weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          10,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
		{
			name:           "rejects primary count above weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          11,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           true,
		},
		{
			name:           "allows canary count at weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          10,
			depType:        "canary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
		{
			name:           "rejects canary count above weighted threshold",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          11,
			depType:        "canary",
			instanceCounts: InstanceCounts{Primary: 9, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           true,
		},
		{
			name:           "promotes zero weighted threshold to one",
			limits:         "premium: 1\n",
			client:         "premium",
			count:          2,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 100, Canary: 1},
			trafficWeight:  TrafficWeight{Primary: 1, Canary: 99},
			want:           true,
		},
		{
			name:           "allows unknown client",
			limits:         "premium: 100\n",
			client:         "unknown",
			count:          1000,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 1},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:           "allows empty deployment type while degraded",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			depType:        "",
			instanceCounts: InstanceCounts{Primary: 1},
			trafficWeight:  TrafficWeight{Primary: 100},
			want:           false,
		},
		{
			name:          "allows when no instances are available",
			limits:        "premium: 100\n",
			client:        "premium",
			count:         1000,
			depType:       "primary",
			trafficWeight: TrafficWeight{Primary: 100},
			want:          false,
		},
		{
			name:           "allows when no traffic weights are available",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			depType:        "primary",
			instanceCounts: InstanceCounts{Primary: 1},
			want:           false,
		},
		{
			name:           "allows canary when no canary pods exist",
			limits:         "premium: 100\n",
			client:         "premium",
			count:          1000,
			depType:        "canary",
			instanceCounts: InstanceCounts{Primary: 9},
			trafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			path := writeTestFile(t, "max_concurrent_requests.yml", tt.limits)
			_, err := NewLimiter(path)
			require.NoError(t, err)

			// When
			got := IsOverLimit(tt.client, tt.count, tt.depType, tt.instanceCounts, tt.trafficWeight)

			// Then
			require.Equal(t, tt.want, got)
		})
	}
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
