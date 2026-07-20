package throttle

import (
	"fmt"
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

const (
	primaryDepType = "primary"
	canaryDepType  = "canary"
)

var (
	depTypeMu sync.RWMutex
	depType   string
)

// DetectDepType reproduces the article's hostname convention.
func DetectDepType(hostname string) string {
	if strings.Contains(hostname, canaryDepType) {
		return canaryDepType
	}
	return primaryDepType
}

// SetDepType records the current deployment type from the local hostname.
func SetDepType() error {
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("read hostname: %w", err)
	}

	depTypeMu.Lock()
	depType = DetectDepType(hostname)
	depTypeMu.Unlock()
	return nil
}

// GetDepType returns the deployment type most recently set by SetDepType.
func GetDepType() string {
	depTypeMu.RLock()
	defer depTypeMu.RUnlock()
	return depType
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
