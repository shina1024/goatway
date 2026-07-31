package config

import (
	"net"
	"regexp"
	"strconv"
)

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
