// Package router matches gateway requests to configured target groups.
package router

import (
	"errors"
	"net"
	"regexp"

	"goatway/internal/scheduler"
)

var (
	ErrNoRoute            = errors.New("router: no route")
	ErrMissingToken       = errors.New("router: missing API token")
	ErrUnknownToken       = errors.New("router: unknown API token")
	ErrClientNotAllowed   = errors.New("router: client is not allowed")
	ErrIPNotAllowed       = errors.New("router: IP address is not allowed")
	ErrInvalidRequestTime = errors.New("router: invalid request time")
)

// ClientType identifies the configured class that owns an API token.
type ClientType string

// Destination is one possible target group for a matching route.
type Destination struct {
	TargetGroupID string
	PathTemplate  string
	Weight        int
}

// Route is the immutable, compiled representation of one configured route.
type Route struct {
	from routeFrom
	to   routeTo
}

type routeFrom struct {
	path                 *regexp.Regexp
	clients              []ClientType
	ipRanges             []*net.IPNet
	hasIPRangeConstraint bool
}

type routeTo struct {
	destinations []Destination
	scheduler    scheduler.Scheduler
}

// Match describes the selected target and every target-specific rewritten path.
type Match struct {
	TargetGroupID string
	RoutedPathMap map[string]string
	ClientType    ClientType
}

// Router routes requests using configuration compiled at construction time.
type Router struct {
	routes           []Route
	tokenToClient    map[string]ClientType
	clientIPResolver ClientIPResolver
}
