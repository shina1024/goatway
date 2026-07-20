package targetgroup

import (
	"fmt"
	"time"

	"goatway/internal/config"
	"goatway/internal/scheduler"
)

// RetryCase specifies the failure condition that permits a retry.
type RetryCase string

const (
	RetryCaseServerError RetryCase = "server_error"
	RetryCaseTimeout     RetryCase = "timeout"
)

// TargetGroup contains target selection and retry policy for one upstream group.
type TargetGroup struct {
	id                   config.TargetGroupID
	targets              []Target
	scheduler            scheduler.Scheduler
	retryTargetMap       map[Target]Target
	maxTryCount          int
	retryCases           []RetryCase
	retryNonIdempotent   bool
	retryBaseInterval    time.Duration
	retryMaxInterval     time.Duration
	retryToTargetGroupID config.TargetGroupID
	retryToTargetGroup   *TargetGroup
	maxIdleConnsPerHost  int
	connectTimeout       time.Duration
	readTimeout          time.Duration
	idleConnTimeout      time.Duration
}

func newTargetGroup(id config.TargetGroupID, groupConfig config.TargetGroupConfig) (*TargetGroup, error) {
	targets := make([]Target, len(groupConfig.Targets))
	weights := make([]int, len(groupConfig.Targets))
	byAddress := make(map[config.TargetAddress]Target, len(groupConfig.Targets))
	for index, targetConfig := range groupConfig.Targets {
		target := newTarget(groupConfig, targetConfig)
		targets[index] = target
		weights[index] = int(target.Weight())
		byAddress[config.TargetAddress(target.Address())] = target
	}

	targetScheduler, err := scheduler.NewScheduler(weights)
	if err != nil {
		return nil, fmt.Errorf("create scheduler for target group %q: %w", id, err)
	}

	retryTargetMap := make(map[Target]Target, len(targets))
	for index, target := range targets {
		retryTarget := targets[(index+1)%len(targets)]
		if retryTo := groupConfig.Targets[index].RetryTo; retryTo != "" {
			var exists bool
			retryTarget, exists = byAddress[retryTo]
			if !exists {
				return nil, &RetryTargetNotFoundError{Address: retryTo}
			}
		}
		retryTargetMap[target] = retryTarget
	}

	retryCases := make([]RetryCase, len(groupConfig.RetryCases))
	for index, retryCase := range groupConfig.RetryCases {
		retryCases[index] = RetryCase(retryCase)
	}

	return &TargetGroup{
		id:                   id,
		targets:              targets,
		scheduler:            targetScheduler,
		retryTargetMap:       retryTargetMap,
		maxTryCount:          groupConfig.EffectiveMaxTryCount(),
		retryCases:           retryCases,
		retryNonIdempotent:   groupConfig.RetryNonIdempotent,
		retryBaseInterval:    groupConfig.EffectiveRetryBaseInterval(),
		retryMaxInterval:     groupConfig.EffectiveRetryMaxInterval(),
		retryToTargetGroupID: groupConfig.RetryToTargetGroupID,
		maxIdleConnsPerHost:  groupConfig.MaxIdleConnsPerHost,
		connectTimeout:       groupConfig.ConnectTimeoutFor(config.TargetConfig{}),
		readTimeout:          groupConfig.ReadTimeoutFor(config.TargetConfig{}),
		idleConnTimeout:      idleConnTimeoutFor(groupConfig, config.TargetConfig{}),
	}, nil
}

func (group *TargetGroup) ID() config.TargetGroupID {
	return group.id
}

func (group *TargetGroup) Targets() []Target {
	return append([]Target(nil), group.targets...)
}

func (group *TargetGroup) MaxTryCount() int {
	return group.maxTryCount
}

func (group *TargetGroup) RetryCases() []RetryCase {
	return append([]RetryCase(nil), group.retryCases...)
}

func (group *TargetGroup) RetryNonIdempotent() bool {
	return group.retryNonIdempotent
}

func (group *TargetGroup) RetryBaseInterval() time.Duration {
	return group.retryBaseInterval
}

func (group *TargetGroup) RetryMaxInterval() time.Duration {
	return group.retryMaxInterval
}

func (group *TargetGroup) RetryToTargetGroupID() config.TargetGroupID {
	return group.retryToTargetGroupID
}

func (group *TargetGroup) RetryToTargetGroup() *TargetGroup {
	return group.retryToTargetGroup
}

func (group *TargetGroup) MaxIdleConnsPerHost() int {
	return group.maxIdleConnsPerHost
}

func (group *TargetGroup) ConnectTimeout() time.Duration {
	return group.connectTimeout
}

func (group *TargetGroup) ReadTimeout() time.Duration {
	return group.readTimeout
}

func (group *TargetGroup) IdleConnTimeout() time.Duration {
	return group.idleConnTimeout
}

// ScheduledTargets returns length targets: first index from scheduler.Fetch(), then retry successors.
func (group *TargetGroup) ScheduledTargets(length int) []Target {
	if length <= 0 {
		return nil
	}

	targets := make([]Target, 0, length)
	target := group.targets[group.scheduler.Fetch()]
	for range length {
		targets = append(targets, target)
		target = group.retryTargetMap[target]
	}
	return targets
}
