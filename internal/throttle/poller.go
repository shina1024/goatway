package throttle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// FallbackThreshold is the number of consecutive fetch failures tolerated before degrading.
const FallbackThreshold = 60

// GetDeploymentState returns one consistent deployment snapshot.
func (t *DeploymentTracker) GetDeploymentState() DeploymentState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// GetInstanceCounts returns the most recently fetched deployment counts.
func (t *DeploymentTracker) GetInstanceCounts() InstanceCounts {
	return t.GetDeploymentState().InstanceCounts
}

// GetTrafficWeight returns the most recently fetched traffic weights.
func (t *DeploymentTracker) GetTrafficWeight() TrafficWeight {
	return t.GetDeploymentState().TrafficWeight
}

func (t *DeploymentTracker) fetch(ctx context.Context, fetcher Fetcher) error {
	counts, err := fetcher.FetchInstanceCounts(ctx)
	if err != nil {
		if isFetchError(err) {
			t.recordFetchError(ctx, err)
			return nil
		}

		var terminatingErr *terminatingError
		if errors.As(err, &terminatingErr) {
			return err
		}
		return err
	}

	weights, err := fetcher.FetchTrafficWeight(ctx)
	if err != nil {
		if isFetchError(err) {
			t.recordFetchError(ctx, err)
			return nil
		}
		return err
	}

	t.mu.Lock()
	t.state = DeploymentState{InstanceCounts: counts, TrafficWeight: weights}
	t.fetchErrCount = 0
	t.mu.Unlock()
	return nil
}

func isFetchError(err error) bool {
	var fetchErr *fetchError
	return errors.As(err, &fetchErr)
}

func (t *DeploymentTracker) recordFetchError(ctx context.Context, err error) {
	t.mu.Lock()
	t.fetchErrCount++
	count := t.fetchErrCount
	if count > FallbackThreshold {
		t.state = DeploymentState{}
	}
	t.mu.Unlock()

	t.logger.WarnContext(
		ctx,
		fmt.Sprintf("(%d/%d) throttle fetch failed", count, FallbackThreshold),
		"error", err,
	)
}

// Poll fetches throttling state on interval until the context ends or fetching terminates.
func (t *DeploymentTracker) Poll(ctx context.Context, fetcher Fetcher, interval time.Duration) {
	if interval <= 0 {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if err := t.fetch(ctx, fetcher); err != nil {
		return
	}

	ticker := time.NewTicker(interval)
polling:
	for {
		select {
		case <-ctx.Done():
			break polling
		case <-ticker.C:
			if err := t.fetch(ctx, fetcher); err != nil {
				break polling
			}
		}
	}
	ticker.Stop()
}
