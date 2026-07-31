package config

import (
	"net"
	"strconv"
)

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
