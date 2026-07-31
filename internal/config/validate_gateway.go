package config

import (
	"net"
	"strconv"

	"goatway/internal/throttle"
)

func (config Config) validateGateway() error {
	gateway := config.Gateway.withDefaults()
	if config.gatewayFilePresent && gateway.SchemaVersion != 1 {
		return invalid("gateway.yml", "schema_version", "schema version must equal 1", strconv.Itoa(gateway.SchemaVersion))
	}
	if gateway.Proxy.MaxResponseBodySizeBytes <= 0 {
		return invalid("gateway.yml", "proxy.max_response_body_size_bytes", "positive max response body size", strconv.FormatInt(gateway.Proxy.MaxResponseBodySizeBytes, 10))
	}
	if gateway.Throttle.FailPolicy != string(throttle.FailOpen) && gateway.Throttle.FailPolicy != string(throttle.FailClosed) {
		return invalid("gateway.yml", "throttle.fail_policy", "fail policy must be fail_open or fail_closed", gateway.Throttle.FailPolicy)
	}
	if gateway.CircuitBreaker.FailureThreshold < 0 {
		return invalid("gateway.yml", "circuit_breaker.failure_threshold", "non-negative failure threshold", strconv.Itoa(gateway.CircuitBreaker.FailureThreshold))
	}
	if gateway.CircuitBreaker.OpenIntervalMs < 0 {
		return invalid("gateway.yml", "circuit_breaker.open_interval_ms", "non-negative open interval", strconv.Itoa(gateway.CircuitBreaker.OpenIntervalMs))
	}
	if gateway.CircuitBreaker.HalfOpenMaxRequests < 0 {
		return invalid("gateway.yml", "circuit_breaker.half_open_max_requests", "non-negative half-open max requests", strconv.Itoa(gateway.CircuitBreaker.HalfOpenMaxRequests))
	}
	if gateway.CircuitBreaker.Enabled && gateway.CircuitBreaker.FailureThreshold < 1 {
		return invalid("gateway.yml", "circuit_breaker.failure_threshold", "positive failure threshold when enabled", strconv.Itoa(gateway.CircuitBreaker.FailureThreshold))
	}
	if gateway.CircuitBreaker.Enabled && gateway.CircuitBreaker.HalfOpenMaxRequests < 1 {
		return invalid("gateway.yml", "circuit_breaker.half_open_max_requests", "positive half-open max requests when enabled", strconv.Itoa(gateway.CircuitBreaker.HalfOpenMaxRequests))
	}
	for index, cidr := range gateway.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return invalid("gateway.yml", "trusted_proxies["+strconv.Itoa(index)+"]", "invalid CIDR", cidr)
		}
	}
	return nil
}
