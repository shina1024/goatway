package router

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver resolves a client IP through configured trusted proxies.
type ClientIPResolver struct {
	trustedProxies []*net.IPNet
}

// NewClientIPResolver compiles configured trusted proxy CIDRs.
func NewClientIPResolver(cidrs []string) (ClientIPResolver, error) {
	trustedProxies := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, trustedProxy, err := net.ParseCIDR(cidr)
		if err != nil {
			return ClientIPResolver{}, fmt.Errorf("parse trusted proxy CIDR %q: %w", cidr, err)
		}
		trustedProxies = append(trustedProxies, trustedProxy)
	}
	return ClientIPResolver{trustedProxies: trustedProxies}, nil
}

// Resolve returns the rightmost untrusted X-Forwarded-For address from a trusted peer.
func (resolver ClientIPResolver) Resolve(request *http.Request) (net.IP, error) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	remoteIP := net.ParseIP(host)
	if remoteIP == nil {
		return nil, fmt.Errorf("parse remote address %q", request.RemoteAddr)
	}
	if !resolver.isTrusted(remoteIP) {
		return remoteIP, nil
	}

	forwardedFor := request.Header.Get("X-Forwarded-For")
	if forwardedFor == "" {
		return remoteIP, nil
	}
	forwardedIPs := make([]net.IP, 0, strings.Count(forwardedFor, ",")+1)
	for _, hop := range strings.Split(forwardedFor, ",") {
		ip := net.ParseIP(strings.TrimSpace(hop))
		if ip == nil {
			return nil, fmt.Errorf("parse X-Forwarded-For hop %q", hop)
		}
		forwardedIPs = append(forwardedIPs, ip)
	}
	for index := len(forwardedIPs) - 1; index >= 0; index-- {
		if !resolver.isTrusted(forwardedIPs[index]) {
			return forwardedIPs[index], nil
		}
	}
	return remoteIP, nil
}

func (resolver ClientIPResolver) hasTrustedProxies() bool {
	return len(resolver.trustedProxies) > 0
}

func (resolver ClientIPResolver) isTrusted(ip net.IP) bool {
	for _, trustedProxy := range resolver.trustedProxies {
		if trustedProxy.Contains(ip) {
			return true
		}
	}
	return false
}
