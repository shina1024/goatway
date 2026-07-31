package throttle

import (
	"fmt"
	"os"
	"sync"

	"goatway/internal/config"
	"gopkg.in/yaml.v3"
)

// Limiter tracks concurrent transfers by client type.
type Limiter struct {
	mu               sync.Mutex
	clientCount      map[string]int
	maxConcurrentMu  sync.RWMutex
	maxConcurrentMap map[string]int
	failPolicy       config.FailPolicy
}

// LimiterOption configures a limiter.
type LimiterOption func(*Limiter)

// WithFailPolicy configures the decision used for degraded deployment state.
func WithFailPolicy(policy config.FailPolicy) LimiterOption {
	return func(limiter *Limiter) {
		limiter.failPolicy = policy
	}
}

// NewLimiter loads the static per-client maximums once from path.
func NewLimiter(path string, options ...LimiterOption) (*Limiter, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the trusted local configuration directory
	if err != nil {
		return nil, fmt.Errorf("read max concurrent requests file %q: %w", path, err)
	}

	limits := make(map[string]int)
	if err := yaml.Unmarshal(data, &limits); err != nil {
		return nil, fmt.Errorf("parse max concurrent requests file %q: %w", path, err)
	}

	return NewLimiterFromLimits(limits, options...), nil
}

// NewLimiterFromLimits creates a limiter from an already-parsed maximums map,
// avoiding a redundant file read when configuration is already loaded.
func NewLimiterFromLimits(limits map[string]int, options ...LimiterOption) *Limiter {
	limiter := &Limiter{
		clientCount:      make(map[string]int),
		maxConcurrentMap: limits,
		failPolicy:       config.FailOpen,
	}
	for _, option := range options {
		if option != nil {
			option(limiter)
		}
	}
	return limiter
}

// Inc records a transfer start and returns the new active count for client.
func (l *Limiter) Inc(client string) int {
	if l == nil {
		return 0
	}

	l.mu.Lock()
	l.clientCount[client]++
	count := l.clientCount[client]
	l.mu.Unlock()
	return count
}

// Dec records a transfer end for client.
func (l *Limiter) Dec(client string) {
	if l == nil {
		return
	}

	l.mu.Lock()
	count, exists := l.clientCount[client]
	if !exists || count <= 1 {
		delete(l.clientCount, client)
		l.mu.Unlock()
		return
	}
	l.clientCount[client] = count - 1
	l.mu.Unlock()
}

// IsOverLimit applies the article's per-pod concurrency calculation.
func (l *Limiter) IsOverLimit(
	client string,
	count int,
	deploymentType string,
	instanceCounts InstanceCounts,
	trafficWeight TrafficWeight,
) bool {
	maximum, found := l.maxConcurrent(client)
	if !found {
		return false
	}
	if deploymentType == "" || instanceCounts.Primary+instanceCounts.Canary == 0 || trafficWeight.Primary+trafficWeight.Canary == 0 {
		return l.failPolicy == config.FailClosed
	}

	if trafficWeight.Canary == 0 {
		if instanceCounts.Primary == 0 {
			return l.failPolicy == config.FailClosed
		}
		threshold := int(int64(maximum) / int64(instanceCounts.Primary))
		if threshold == 0 {
			threshold = 1
		}
		return count > threshold
	}

	weight, instances := trafficWeight.Canary, instanceCounts.Canary
	if deploymentType == primaryDeployment {
		weight, instances = trafficWeight.Primary, instanceCounts.Primary
	}
	if instances == 0 {
		return l.failPolicy == config.FailClosed
	}
	threshold := int(int64(maximum) * int64(weight) / 100 / int64(instances))
	if threshold == 0 {
		threshold = 1
	}
	return count > threshold
}

func (l *Limiter) maxConcurrent(client string) (int, bool) {
	if l == nil {
		return 0, false
	}

	l.maxConcurrentMu.RLock()
	defer l.maxConcurrentMu.RUnlock()
	maximum, found := l.maxConcurrentMap[client]
	return maximum, found
}
