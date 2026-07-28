package router

import (
	"fmt"
	"net/http"
	"regexp"

	"goatway/internal/config"
	"goatway/internal/scheduler"
)

// New compiles immutable routes from validated configuration.
func New(configuration config.Config) (*Router, error) {
	clientIPResolver, err := NewClientIPResolver(configuration.Gateway.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("create client IP resolver: %w", err)
	}

	routes := make([]Route, 0, len(configuration.Routes))
	for index, configuredRoute := range configuration.Routes {
		route, err := compileRoute(configuredRoute, configuration.IPRangeGroups)
		if err != nil {
			return nil, fmt.Errorf("compile route %d: %w", index, err)
		}
		routes = append(routes, route)
	}

	return &Router{
		routes:           routes,
		tokenToClient:    tokenClients(configuration.APIClientTokens),
		clientIPResolver: clientIPResolver,
	}, nil
}

func compileRoute(configured config.RouteConfig, rangeGroups map[string][]string) (Route, error) {
	path, err := regexp.Compile(configured.From.Path)
	if err != nil {
		return Route{}, fmt.Errorf("compile path: %w", err)
	}
	ipRanges, err := resolveIPRanges(configured.From.IPRangeGroups, rangeGroups)
	if err != nil {
		return Route{}, err
	}

	destinations := make([]Destination, len(configured.To.Destinations))
	weights := make([]int, len(configured.To.Destinations))
	for index, configuredDestination := range configured.To.Destinations {
		destinations[index] = Destination{
			TargetGroupID: string(configuredDestination.TargetGroup),
			PathTemplate:  configuredDestination.Path,
			Weight:        int(configuredDestination.Weight),
		}
		weights[index] = int(configuredDestination.Weight)
	}
	scheduled, err := scheduler.NewScheduler(weights)
	if err != nil {
		return Route{}, fmt.Errorf("create destination scheduler: %w", err)
	}

	clients := make([]ClientType, len(configured.From.Clients))
	for index, client := range configured.From.Clients {
		clients[index] = ClientType(client)
	}
	return Route{
		from: routeFrom{
			path:                 path,
			clients:              clients,
			ipRanges:             ipRanges,
			hasIPRangeConstraint: len(configured.From.IPRangeGroups) > 0,
		},
		to: routeTo{destinations: destinations, scheduler: scheduled},
	}, nil
}

// Route returns the first configured path match that satisfies its constraints.
// A failed constraint returns its authorization error instead of trying later routes.
func (router *Router) Route(request *http.Request) (Match, error) {
	for _, route := range router.routes {
		if !route.from.path.MatchString(request.URL.Path) {
			continue
		}
		client, err := route.authorize(request, router.tokenToClient)
		if err != nil {
			return Match{}, err
		}
		if err := route.allowIP(request, router.clientIPResolver); err != nil {
			return Match{}, err
		}

		selected := route.to.destinations[route.to.scheduler.Fetch()]
		paths := make(map[string]string, len(route.to.destinations))
		for _, destination := range route.to.destinations {
			paths[destination.TargetGroupID] = route.from.path.ReplaceAllString(request.URL.Path, destination.PathTemplate)
		}
		return Match{TargetGroupID: selected.TargetGroupID, RoutedPathMap: paths, ClientType: client}, nil
	}
	return Match{}, ErrNoRoute
}
