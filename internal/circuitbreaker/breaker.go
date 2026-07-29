// Package circuitbreaker controls requests to unhealthy target groups.
package circuitbreaker

import (
	"sync"
	"time"
)

type state uint8

const (
	stateClosed state = iota
	stateOpen
	stateHalfOpen
)

// Config controls breaker state transitions.
type Config struct {
	FailureThreshold    int
	OpenInterval        time.Duration
	HalfOpenMaxRequests int
}

// Breaker tracks consecutive failures for one target group.
type Breaker struct {
	mu             sync.Mutex
	state          state
	failures       int
	threshold      int
	openInterval   time.Duration
	halfOpenMax    int
	halfOpenActive int
	halfOpenTotal  int
	openedAt       time.Time
	now            func() time.Time
}

// New creates a breaker with the supplied transition configuration.
func New(config Config) *Breaker {
	return newBreaker(config, time.Now)
}

func newBreaker(config Config, now func() time.Time) *Breaker {
	return &Breaker{
		threshold:    config.FailureThreshold,
		openInterval: config.OpenInterval,
		halfOpenMax:  config.HalfOpenMaxRequests,
		now:          now,
	}
}

// Allow reports whether an attempt may proceed.
func (breaker *Breaker) Allow() bool {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	if breaker.state == stateOpen {
		if breaker.now().Sub(breaker.openedAt) < breaker.openInterval {
			return false
		}
		breaker.state = stateHalfOpen
		breaker.halfOpenActive = 0
		breaker.halfOpenTotal = 0
	}
	if breaker.state == stateHalfOpen {
		if breaker.halfOpenTotal >= breaker.halfOpenMax {
			return false
		}
		breaker.halfOpenActive++
		breaker.halfOpenTotal++
	}
	return true
}

// RecordSuccess resets failures and closes a half-open breaker.
func (breaker *Breaker) RecordSuccess() {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	if breaker.state == stateOpen {
		return
	}
	if breaker.state == stateHalfOpen {
		if breaker.halfOpenActive > 0 {
			breaker.halfOpenActive--
		}
		if breaker.halfOpenActive > 0 {
			return
		}
	}
	breaker.close()
}

// RecordFailure increments failures or reopens a failed half-open probe.
func (breaker *Breaker) RecordFailure() {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()

	if breaker.state == stateHalfOpen {
		breaker.open()
		return
	}
	if breaker.state != stateClosed {
		return
	}
	breaker.failures++
	if breaker.failures >= breaker.threshold {
		breaker.open()
	}
}

func (breaker *Breaker) open() {
	breaker.state = stateOpen
	breaker.failures = 0
	breaker.halfOpenActive = 0
	breaker.halfOpenTotal = 0
	breaker.openedAt = breaker.now()
}

func (breaker *Breaker) close() {
	breaker.state = stateClosed
	breaker.failures = 0
	breaker.halfOpenActive = 0
	breaker.halfOpenTotal = 0
}
