package throttle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	tracker := NewDeploymentTracker()
	setPollingState(tracker, InstanceCounts{Primary: 20, Canary: 1}, TrafficWeight{Primary: 90, Canary: 10}, 0)
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
		require.NoError(t, tracker.fetch(context.Background(), fetcher))
	}

	// Then
	require.Equal(t, InstanceCounts{Primary: 20, Canary: 1}, tracker.GetInstanceCounts())
	require.Equal(t, FallbackThreshold, getFetchErrCount(tracker))

	// When
	require.NoError(t, tracker.fetch(context.Background(), fetcher))

	// Then
	require.Equal(t, InstanceCounts{}, tracker.GetInstanceCounts())
	require.Equal(t, DeploymentState{}, tracker.GetDeploymentState())
	require.Equal(t, FallbackThreshold+1, getFetchErrCount(tracker))
}

func Test_fetch_resets_error_count_when_both_fetches_succeed(t *testing.T) {
	// Given
	tracker := NewDeploymentTracker()
	setPollingState(tracker, InstanceCounts{}, TrafficWeight{}, 1)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{Primary: 9, Canary: 1}, nil
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{Primary: 90, Canary: 10}, nil
		},
	}

	// When
	err := tracker.fetch(context.Background(), fetcher)

	// Then
	require.NoError(t, err)
	require.Equal(t, InstanceCounts{Primary: 9, Canary: 1}, tracker.GetInstanceCounts())
	require.Equal(t, TrafficWeight{Primary: 90, Canary: 10}, tracker.GetTrafficWeight())
	require.Zero(t, getFetchErrCount(tracker))
}

func Test_fetch_does_not_publish_partial_state_when_weight_fetch_fails(t *testing.T) {
	// Given
	tracker := NewDeploymentTracker()
	want := DeploymentState{
		InstanceCounts: InstanceCounts{Primary: 9, Canary: 1},
		TrafficWeight:  TrafficWeight{Primary: 90, Canary: 10},
	}
	setPollingState(tracker, want.InstanceCounts, want.TrafficWeight, 0)
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{Primary: 1, Canary: 9}, nil
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{}, &FetchError{Err: errors.New("traffic unavailable")}
		},
	}

	// When
	err := tracker.fetch(context.Background(), fetcher)

	// Then
	require.NoError(t, err)
	require.Equal(t, want, tracker.GetDeploymentState())
}

func Test_DeploymentTracker_uses_injected_logger_for_fetch_errors(t *testing.T) {
	// Given
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	tracker := NewDeploymentTracker(WithLogger(logger))
	fetcher := stubFetcher{
		fetchInstanceCounts: func(context.Context) (InstanceCounts, error) {
			return InstanceCounts{}, &FetchError{Err: errors.New("deployment unavailable")}
		},
		fetchTrafficWeight: func(context.Context) (TrafficWeight, error) {
			return TrafficWeight{}, nil
		},
	}

	// When
	err := tracker.fetch(context.Background(), fetcher)

	// Then
	require.NoError(t, err)
	require.Contains(t, logs.String(), "throttle fetch failed")
	require.Contains(t, logs.String(), "deployment unavailable")
}

func Test_NewDeploymentTracker_uses_default_logger_when_nil_is_injected(t *testing.T) {
	// Given
	tracker := NewDeploymentTracker(WithLogger(nil))

	// Then
	require.Same(t, slog.Default(), tracker.logger)
}

func Test_Poll_fetches_immediately_before_first_interval(t *testing.T) {
	// Given
	tracker := NewDeploymentTracker()
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
		tracker.Poll(ctx, fetcher, time.Hour)
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
	}, tracker.GetDeploymentState())
}

func Test_Poll_stops_when_instance_fetch_returns_terminating_error(t *testing.T) {
	// Given
	tracker := NewDeploymentTracker()
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
		tracker.Poll(ctx, fetcher, time.Nanosecond)
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

func setPollingState(tracker *DeploymentTracker, counts InstanceCounts, weight TrafficWeight, errCount int) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.state = DeploymentState{InstanceCounts: counts, TrafficWeight: weight}
	tracker.fetchErrCount = errCount
}

func getFetchErrCount(tracker *DeploymentTracker) int {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.fetchErrCount
}
