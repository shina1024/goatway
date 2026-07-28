package router

import (
	"fmt"
	"net"
	"net/http"
)

func resolveIPRanges(groupNames []string, groups map[string][]string) ([]*net.IPNet, error) {
	if len(groupNames) == 0 {
		return nil, nil
	}

	ranges := make([]*net.IPNet, 0)
	for _, groupName := range groupNames {
		cidrs, exists := groups[groupName]
		if !exists {
			return nil, fmt.Errorf("unknown IP range group %q", groupName)
		}
		for _, cidr := range cidrs {
			_, parsed, err := net.ParseCIDR(cidr)
			if err != nil {
				return nil, fmt.Errorf("parse CIDR %q: %w", cidr, err)
			}
			ranges = append(ranges, parsed)
		}
	}
	return ranges, nil
}

func (route Route) allowIP(request *http.Request, resolver ClientIPResolver) error {
	if !route.from.hasIPRangeConstraint {
		return nil
	}
	if resolver.hasTrustedProxies() {
		ip, err := resolver.Resolve(request)
		if err != nil {
			return ErrIPNotAllowed
		}
		for _, allowedRange := range route.from.ipRanges {
			if allowedRange.Contains(ip) {
				return nil
			}
		}
		return ErrIPNotAllowed
	}

	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ErrIPNotAllowed
	}
	for _, allowedRange := range route.from.ipRanges {
		if allowedRange.Contains(ip) {
			return nil
		}
	}
	return ErrIPNotAllowed
}
