package throttle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubFetcher struct {
	fetchInstanceCounts func(context.Context) (InstanceCounts, error)
	fetchTrafficWeight  func(context.Context) (TrafficWeight, error)
}

func (f stubFetcher) FetchInstanceCounts(ctx context.Context) (InstanceCounts, error) {
	return f.fetchInstanceCounts(ctx)
}

func (f stubFetcher) FetchTrafficWeight(ctx context.Context) (TrafficWeight, error) {
	return f.fetchTrafficWeight(ctx)
}

func Test_fetch_degrades_instance_counts_after_more_than_fallback_threshold_errors(t *testing.T) {
	// Given
	resetPollingState()
	setPollingState(InstanceCounts{Primary: 20, Canary: 1}, TrafficWeight{Primary: 90, Canary: 10}, 0)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{}, &FetchError{Err: errors.New("deployment unavailable")}
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{}, errors.New("traffic weights must not be fetched after instance failure")
		},
	}

	// When
	for range FallbackThreshold {
		require.NoError(t, fetch(context.Background(), fetcher))
	}

	// Then
	require.Equal(t, InstanceCounts{Primary: 20, Canary: 1}, GetInstanceCounts())
	require.Equal(t, FallbackThreshold, getFetchErrCount())

	// When
	require.NoError(t, fetch(context.Background(), fetcher))

	// Then
	require.Equal(t, InstanceCounts{}, GetInstanceCounts())
	require.Equal(t, DeploymentState{}, GetDeploymentState())
	require.Equal(t, FallbackThreshold+1, getFetchErrCount())
}

func Test_fetch_resets_error_count_when_both_fetches_succeed(t *testing.T) {
	// Given
	resetPollingState()
	setPollingState(InstanceCounts{}, TrafficWeight{}, 1)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{Primary: 9, Canary: 1}, nil
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{Primary: 90, Canary: 10}, nil
		},
	}

	// When
	err := fetch(context.Background(), fetcher)

	// Then
	require.NoError(t, err)
	require.Equal(t, InstanceCounts{Primary: 9, Canary: 1}, GetInstanceCounts())
	require.Equal(t, TrafficWeight{Primary: 90, Canary: 10}, GetTrafficWeight())
	require.Zero(t, getFetchErrCount())
}

func Test_fetch_does_not_publish_partial_state_when_weight_fetch_fails(t *testing.T) {
	// Given
	resetPollingState()
	want := DeploymentState{
		InstanceCounts: InstanceCounts{Primary: 9, Canary: 1},
		TrafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
	}
	setPollingState(want.InstanceCounts, want.TrafficWeight, 0)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{Primary: 1, Canary: 9}, nil
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{}, &FetchError{Err: errors.New("traffic unavailable")}
		},
	}

	// When
	err := fetch(context.Background(), fetcher)

	// Then
	require.NoError(t, err)
	require.Equal(t, want, GetDeploymentState())
}

func Test_Poll_fetches_immediately_before_first_interval(t *testing.T) {
	// Given
	resetPollingState()
	fetched := make(chan struct{}, 1)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			fetched <- struct{}{}
			return InstanceCounts{Primary: 1}, nil
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{Primary: 100}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Poll(ctx, fetcher, time.Hour)
		close(done)
	}()

	// When
	select {
	case <-fetched:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("poller waited for the first ticker interval")
	}

	// Then
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not stop after cancellation")
	}
	require.Equal(t, DeploymentState{
		InstanceCounts: InstanceCounts{Primary: 1},
		TrafficWeight:  TrafficWeight{Primary: 100},
	}, GetDeploymentState())
}

func Test_Poll_stops_when_instance_fetch_returns_terminating_error(t *testing.T) {
	// Given
	resetPollingState()
	started := make(chan struct{}, 1)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			started <- struct{}{}
			return InstanceCounts{}, &TerminatingError{Err: errors.New("terminating")}
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		Poll(ctx, fetcher, time.Nanosecond)
		close(done)
	}()

	// When
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("poller did not fetch before context expired")
	}

	// Then
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("poller did not stop after terminating error")
	}
}

func resetPollingState() {
	stateMu.Lock()
	defer stateMu.Unlock()
	deploymentState = DeploymentState{}
	fetchErrCount = 0
}

func setPollingState(counts InstanceCounts, weight TrafficWeight, errCount int) {
	stateMu.Lock()
	defer stateMu.Unlock()
	deploymentState = DeploymentState{InstanceCounts: counts, TrafficWeight: weight}
	fetchErrCount = errCount
}

func getFetchErrCount() int {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return fetchErrCount
}
