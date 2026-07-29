package proxy

import (
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"goatway/internal/targetgroup"
)

const (
	maxRetryIntervalMultiplier = 10
	maxDuration                = time.Duration(1<<63 - 1)
)

type retryAttempt struct {
	target targetgroup.Target
	group  *targetgroup.TargetGroup
}

func retrySchedule(group *targetgroup.TargetGroup, method string) []retryAttempt {
	maxTryCount := group.MaxTryCount()
	if !group.RetryNonIdempotent() && (method == http.MethodPost || method == http.MethodPatch) {
		maxTryCount = 1
	}
	if maxTryCount < 1 {
		return nil
	}
	if retryGroup := group.RetryToTargetGroup(); retryGroup != nil {
		first := group.ScheduledTargets(1)
		remaining := retryGroup.ScheduledTargets(maxTryCount - 1)
		attempts := make([]retryAttempt, 0, maxTryCount)
		for _, target := range first {
			attempts = append(attempts, retryAttempt{target: target, group: group})
		}
		for _, target := range remaining {
			attempts = append(attempts, retryAttempt{target: target, group: retryGroup})
		}
		return attempts
	}
	targets := group.ScheduledTargets(maxTryCount)
	attempts := make([]retryAttempt, len(targets))
	for index, target := range targets {
		attempts[index] = retryAttempt{target: target, group: group}
	}
	return attempts
}

func retryable(group *targetgroup.TargetGroup, result AttemptResult, attemptErr error) bool {
	var retryCase targetgroup.RetryCase
	if attemptErr != nil {
		if result.ErrClass == ErrClassTimeout {
			retryCase = targetgroup.RetryCaseTimeout
		}
	} else if result.StatusCode >= http.StatusInternalServerError && result.StatusCode < 600 {
		retryCase = targetgroup.RetryCaseServerError
	}
	return slices.Contains(group.RetryCases(), retryCase)
}

func retryBackoffCap(baseInterval, maxInterval time.Duration, tryCount int) time.Duration {
	if baseInterval <= 0 {
		return 0
	}
	if maxInterval <= 0 {
		if baseInterval > maxDuration/maxRetryIntervalMultiplier {
			maxInterval = maxDuration
		} else {
			maxInterval = maxRetryIntervalMultiplier * baseInterval
		}
	}
	if baseInterval >= maxInterval {
		return maxInterval
	}
	interval := baseInterval
	for range tryCount {
		if interval >= maxInterval || interval > maxInterval/2 {
			return maxInterval
		}
		interval *= 2
	}
	return interval
}

func fullJitter(cap time.Duration) time.Duration {
	if cap <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(cap) + 1)) //nolint:gosec // article-faithful retry jitter does not require cryptographic randomness
}
