package config

import (
	"net"
	"regexp"
	"strconv"

	"goatway/internal/throttle"
)

func (config Config) Validate() error {
	if err := config.validateTargetGroups(); err != nil {
		return err
	}
	if err := config.validateRoutes(); err != nil {
		return err
	}
	if err := config.validateCrossGroupRetries(); err != nil {
		return err
	}
	if err := config.validateIPRangeGroups(); err != nil {
		return err
	}
	if err := config.validateTokens(); err != nil {
		return err
	}
	if err := config.validateGateway(); err != nil {
		return err
	}
	if config.Deployment.PrimaryWeight < 0 || config.Deployment.CanaryWeight < 0 {
		return invalid("deployment.yml", "weights", "negative weight", "")
	}
	if config.Deployment.PrimaryPods < 0 || config.Deployment.CanaryPods < 0 {
		return invalid("deployment.yml", "pods", "negative pod count", "")
	}
	totalWeight := config.Deployment.PrimaryWeight + config.Deployment.CanaryWeight
	if totalWeight != 0 && totalWeight != 100 {
		return invalid("deployment.yml", "weights", "traffic weights must total 100 or 0", "")
	}
	for client, maximum := range config.MaxConcurrentRequests {
		if maximum < 0 {
			return invalid("max_concurrent_requests.yml", string(client), "negative max concurrent requests", "")
		}
	}
	return nil
}

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
	if gateway.CircuitBreaker.OpenIntervalMS < 0 {
		return invalid("gateway.yml", "circuit_breaker.open_interval_ms", "non-negative open interval", strconv.Itoa(gateway.CircuitBreaker.OpenIntervalMS))
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

func (config Config) validateTargetGroups() error {
	for groupID, group := range config.TargetGroups {
		if len(group.Targets) == 0 {
			return invalid("target_groups.yml", string(groupID)+".targets", "empty target list", "")
		}
		if group.MaxTryCount < 0 {
			return invalid("target_groups.yml", string(groupID)+".max_try_count", "negative max try count", "")
		}
		if group.Scheme != "" && group.Scheme != "http" && group.Scheme != "https" {
			return invalid("target_groups.yml", string(groupID)+".scheme", "invalid scheme", group.Scheme)
		}
		if group.ConnectTimeout < 0 || group.ReadTimeout < 0 || group.IdleConnTimeout < 0 {
			return invalid("target_groups.yml", string(groupID)+".timeouts", "negative timeout", "")
		}
		if group.RetryBaseInterval < 0 || group.RetryMaxInterval < 0 {
			return invalid("target_groups.yml", string(groupID)+".retry_interval", "negative retry interval", "")
		}
		if err := validateWeights("target_groups.yml", string(groupID)+".targets", targetWeights(group.Targets)); err != nil {
			return err
		}
		addresses := make(map[TargetAddress]struct{}, len(group.Targets))
		for index, target := range group.Targets {
			if target.Host == "" {
				return invalid("target_groups.yml", string(groupID)+".targets["+strconv.Itoa(index)+"].host", "empty host", "")
			}
			if target.Port <= 0 {
				return invalid("target_groups.yml", string(groupID)+".targets["+strconv.Itoa(index)+"].port", "non-positive port", "")
			}
			if target.ConnectTimeout < 0 || target.ReadTimeout < 0 || target.IdleConnTimeout < 0 {
				return invalid("target_groups.yml", string(groupID)+".targets["+strconv.Itoa(index)+"]", "negative timeout", "")
			}
			if target.Scheme != "" && target.Scheme != "http" && target.Scheme != "https" {
				return invalid("target_groups.yml", string(groupID)+".targets["+strconv.Itoa(index)+"].scheme", "invalid scheme", target.Scheme)
			}
			addresses[TargetAddress(net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))] = struct{}{}
		}
		for index, target := range group.Targets {
			if target.RetryTo == "" {
				continue
			}
			if _, exists := addresses[target.RetryTo]; !exists {
				return invalid("target_groups.yml", string(groupID)+".targets["+strconv.Itoa(index)+"].retry_to", "unknown retry target", string(target.RetryTo))
			}
		}
		for _, retryCase := range group.RetryCases {
			if retryCase != "server_error" && retryCase != "timeout" {
				return invalid("target_groups.yml", string(groupID)+".retry_cases", "invalid retry case", retryCase)
			}
		}
	}
	return nil
}

func (config Config) validateRoutes() error {
	for routeIndex, route := range config.Routes {
		if len(route.To.Destinations) == 0 {
			return invalid("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].to.destinations", "empty destination list", "")
		}
		if _, err := regexp.Compile(route.From.Path); err != nil {
			return invalid("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].from.path", "invalid route regexp", route.From.Path)
		}
		if err := validateWeights("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].to.destinations", destinationWeights(route.To.Destinations)); err != nil {
			return err
		}
		destinations := make(map[TargetGroupID]struct{}, len(route.To.Destinations))
		for _, destination := range route.To.Destinations {
			if _, exists := config.TargetGroups[destination.TargetGroup]; !exists {
				return invalid("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].to.destinations", "unknown target group", string(destination.TargetGroup))
			}
			if _, exists := destinations[destination.TargetGroup]; exists {
				return invalid("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].to.destinations", "duplicate target group", string(destination.TargetGroup))
			}
			destinations[destination.TargetGroup] = struct{}{}
		}
	}
	return nil
}

func (config Config) validateCrossGroupRetries() error {
	for groupID, group := range config.TargetGroups {
		retryGroupID := group.RetryToTargetGroupID
		if retryGroupID == "" {
			continue
		}
		if _, exists := config.TargetGroups[retryGroupID]; !exists {
			return invalid("target_groups.yml", string(groupID)+".retry_to_target_group_id", "unknown retry target group", string(retryGroupID))
		}
		for routeIndex, route := range config.Routes {
			usesGroup := false
			hasRetryDestination := false
			for _, destination := range route.To.Destinations {
				usesGroup = usesGroup || destination.TargetGroup == groupID
				hasRetryDestination = hasRetryDestination || destination.TargetGroup == retryGroupID
			}
			if usesGroup && !hasRetryDestination {
				return invalid("routes.yml", "routes["+strconv.Itoa(routeIndex)+"].to.destinations", "retry target group missing route destination", string(retryGroupID))
			}
		}
	}
	return nil
}

func (config Config) validateIPRangeGroups() error {
	for groupName, ranges := range config.IPRangeGroups {
		for _, cidr := range ranges {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return invalid("ip_range_groups.yml", groupName, "invalid CIDR", cidr)
			}
		}
	}
	return nil
}

func (config Config) validateTokens() error {
	owners := make(map[string]ClientType)
	for clientType, tokens := range config.APIClientTokens {
		for _, token := range tokens {
			owner, exists := owners[token]
			if exists && owner != clientType {
				return invalid("api_client_tokens.yml", string(clientType), "duplicate token", "<redacted>")
			}
			owners[token] = clientType
		}
	}
	return nil
}

func validateWeights(file, field string, weights []Weight) error {
	hasZero := false
	hasPositive := false
	for _, weight := range weights {
		if weight < 0 {
			return invalid(file, field, "negative weight", "")
		}
		hasZero = hasZero || weight == 0
		hasPositive = hasPositive || weight > 0
	}
	if hasZero && hasPositive {
		return invalid(file, field, "mixed weighted and nonweighted", "")
	}
	return nil
}

func targetWeights(targets []TargetConfig) []Weight {
	weights := make([]Weight, len(targets))
	for index, target := range targets {
		weights[index] = target.Weight
	}
	return weights
}

func destinationWeights(destinations []DestinationConfig) []Weight {
	weights := make([]Weight, len(destinations))
	for index, destination := range destinations {
		weights[index] = destination.Weight
	}
	return weights
}
