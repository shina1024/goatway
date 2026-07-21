package targetgroup

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"goatway/internal/config"
)

const defaultIdleConnTimeout = 90 * time.Second

// Target is an immutable, fully resolved upstream target.
type Target struct {
	host            string
	scheme          string
	port            int
	connectTimeout  time.Duration
	readTimeout     time.Duration
	idleConnTimeout time.Duration
	weight          config.Weight
}

func newTarget(group config.TargetGroupConfig, target config.TargetConfig) Target {
	return Target{
		host:            target.Host,
		scheme:          group.SchemeFor(target),
		port:            target.Port,
		connectTimeout:  group.ConnectTimeoutFor(target),
		readTimeout:     group.ReadTimeoutFor(target),
		idleConnTimeout: idleConnTimeoutFor(group, target),
		weight:          target.Weight,
	}
}

func (target Target) Address() string {
	return net.JoinHostPort(target.host, strconv.Itoa(target.port))
}

func (target Target) Host() string {
	return target.host
}

func (target Target) Scheme() string {
	return target.scheme
}

func (target Target) Port() int {
	return target.port
}

func (target Target) ConnectTimeout() time.Duration {
	return target.connectTimeout
}

func (target Target) ReadTimeout() time.Duration {
	return target.readTimeout
}

func (target Target) IdleConnTimeout() time.Duration {
	return target.idleConnTimeout
}

func (target Target) Weight() config.Weight {
	return target.weight
}

func idleConnTimeoutFor(group config.TargetGroupConfig, target config.TargetConfig) time.Duration {
	if target.IdleConnTimeout != 0 {
		return time.Duration(target.IdleConnTimeout) * time.Millisecond
	}
	if group.IdleConnTimeout != 0 {
		return time.Duration(group.IdleConnTimeout) * time.Millisecond
	}
	return defaultIdleConnTimeout
}

// RetryTargetNotFoundError identifies a retry_to target missing from its group.
type RetryTargetNotFoundError struct {
	Address config.TargetAddress
}

func (err *RetryTargetNotFoundError) Error() string {
	return fmt.Sprintf("retry target %q was not found", err.Address)
}
