package throttle

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// InstanceCounts is the number of running pods for each deployment type.
type InstanceCounts struct {
	Primary int
	Canary  int
}

// TrafficWeight is the Istio-equivalent percentage weight for each deployment type.
type TrafficWeight struct {
	Primary int
	Canary  int
}

// DeploymentState is one atomically published view of instance counts and traffic weights.
type DeploymentState struct {
	InstanceCounts InstanceCounts
	TrafficWeight  TrafficWeight
}

// DeploymentTracker owns the local deployment type and fetched deployment state.
type DeploymentTracker struct {
	mu            sync.RWMutex
	state         DeploymentState
	fetchErrCount int
	depType       string
	logger        *slog.Logger
}

// DeploymentTrackerOption configures a deployment tracker.
type DeploymentTrackerOption func(*DeploymentTracker)

// WithLogger configures the logger used for recoverable fetch failures.
func WithLogger(logger *slog.Logger) DeploymentTrackerOption {
	return func(tracker *DeploymentTracker) {
		tracker.logger = logger
	}
}

// NewDeploymentTracker creates an empty deployment tracker.
func NewDeploymentTracker(options ...DeploymentTrackerOption) *DeploymentTracker {
	tracker := &DeploymentTracker{logger: slog.Default()}
	for _, option := range options {
		if option != nil {
			option(tracker)
		}
	}
	if tracker.logger == nil {
		tracker.logger = slog.Default()
	}
	return tracker
}

const (
	primaryDepType = "primary"
	canaryDepType  = "canary"
)

// DetectDepType reproduces the article's hostname convention.
func DetectDepType(hostname string) string {
	if strings.Contains(hostname, canaryDepType) {
		return canaryDepType
	}
	return primaryDepType
}

// SetDepType records the current deployment type from the local hostname.
func (t *DeploymentTracker) SetDepType() error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("read hostname: %w", err)
	}

	t.mu.Lock()
	t.depType = DetectDepType(hostname)
	t.mu.Unlock()
	return nil
}

// GetDepType returns the deployment type most recently set by SetDepType.
func (t *DeploymentTracker) GetDepType() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.depType
}

// FetchError marks a recoverable fetch failure for errors.As callers.
type FetchError struct {
	Err error
}

func (e *FetchError) Error() string {
	if e == nil || e.Err == nil {
		return "throttle fetch error"
	}
	return "throttle fetch error: " + e.Err.Error()
}

func (e *FetchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type fetchError = FetchError

// TerminatingError marks a terminating workload, which ends polling.
type TerminatingError struct {
	Err error
}

func (e *TerminatingError) Error() string {
	if e == nil || e.Err == nil {
		return "throttle terminating error"
	}
	return "throttle terminating error: " + e.Err.Error()
}

func (e *TerminatingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type terminatingError = TerminatingError
