package throttle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// FallbackThreshold is the number of consecutive fetch failures tolerated before degrading.
const FallbackThreshold = 60

var (
	stateMu        sync.RWMutex
	instanceCounts InstanceCounts
	trafficWeight  TrafficWeight
	fetchErrCount  int
)

// GetInstanceCounts returns the most recently fetched deployment counts.
func GetInstanceCounts() InstanceCounts {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return instanceCounts
}

// GetTrafficWeight returns the most recently fetched traffic weights.
func GetTrafficWeight() TrafficWeight {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return trafficWeight
}

func fetch(ctx context.Context, fetcher Fetcher) error {
	counts, err := fetcher.FetchInstanceCounts(ctx)
	if err != nil {
		if isFetchError(err) {
			recordFetchError(ctx, err)
			return nil
		}

		var terminatingErr *terminatingError
		if errors.As(err, &terminatingErr) {
			return err
		}
		return err
	}

	stateMu.Lock()
	instanceCounts = counts
	stateMu.Unlock()

	weights, err := fetcher.FetchTrafficWeight(ctx)
	if err != nil {
		if isFetchError(err) {
			recordFetchError(ctx, err)
			return nil
		}
		return err
	}

	stateMu.Lock()
	trafficWeight = weights
	fetchErrCount = 0
	stateMu.Unlock()
	return nil
}

func isFetchError(err error) bool {
	var fetchErr *fetchError
	return errors.As(err, &fetchErr)
}

func recordFetchError(ctx context.Context, err error) {
	stateMu.Lock()
	fetchErrCount++
	count := fetchErrCount
	if count > FallbackThreshold {
		instanceCounts = InstanceCounts{}
	}
	stateMu.Unlock()

	slog.WarnContext(
		ctx,
		fmt.Sprintf("(%d/%d) throttle fetch failed", count, FallbackThreshold),
		slog.Any("error", err),
	)
}

// Poll fetches throttling state on interval until the context ends or fetching terminates.
func Poll(ctx context.Context, fetcher Fetcher, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
polling:
	for {
		select {
		case <-ctx.Done():
			break polling
		case <-ticker.C:
			if err := fetch(ctx, fetcher); err != nil {
				break polling
			}
		}
	}
	ticker.Stop()
}
