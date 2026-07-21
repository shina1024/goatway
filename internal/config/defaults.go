package config

import "time"

const (
	defaultConnectTimeout    = time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultRetryBaseInterval = 50 * time.Millisecond
	defaultRetryMaxInterval  = 10 * defaultRetryBaseInterval
)

func (group TargetGroupConfig) EffectiveMaxTryCount() int {
	if group.MaxTryCount == 0 {
		return len(group.Targets)
	}
	return group.MaxTryCount
}

func (group TargetGroupConfig) ConnectTimeoutFor(target TargetConfig) time.Duration {
	return resolveTimeout(target.ConnectTimeout, group.ConnectTimeout, defaultConnectTimeout)
}

func (group TargetGroupConfig) SchemeFor(target TargetConfig) string {
	if target.Scheme != "" {
		return target.Scheme
	}
	if group.Scheme != "" {
		return group.Scheme
	}
	return "http"
}

func (group TargetGroupConfig) ReadTimeoutFor(target TargetConfig) time.Duration {
	return resolveTimeout(target.ReadTimeout, group.ReadTimeout, defaultReadTimeout)
}

func (group TargetGroupConfig) IdleConnTimeoutFor(target TargetConfig) time.Duration {
	return resolveTimeout(target.IdleConnTimeout, group.IdleConnTimeout, 0)
}

func (group TargetGroupConfig) EffectiveRetryBaseInterval() time.Duration {
	return durationOrDefault(group.RetryBaseInterval, defaultRetryBaseInterval)
}

func (group TargetGroupConfig) EffectiveRetryMaxInterval() time.Duration {
	if group.RetryMaxInterval != 0 {
		return durationOrDefault(group.RetryMaxInterval, defaultRetryMaxInterval)
	}
	if group.RetryBaseInterval == 0 {
		return defaultRetryMaxInterval
	}
	return 10 * group.EffectiveRetryBaseInterval()
}

func resolveTimeout(target, group Milliseconds, fallback time.Duration) time.Duration {
	if target != 0 {
		return time.Duration(target) * time.Millisecond
	}
	return durationOrDefault(group, fallback)
}

func durationOrDefault(value Milliseconds, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}
