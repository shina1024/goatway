package proxy

import (
	"net/http"
	"strings"

	"goatway/internal/header"
)

func copyForwardableHeaders(destination, source http.Header) {
	for name, values := range source {
		destination[name] = append([]string(nil), values...)
	}
}

func filteredRequestHeaders(h http.Header) http.Header {
	result := withoutHopByHopHeaders(h)
	removeHeaders(result, header.APIToken, "Authorization", "Cookie", header.RequestTime, header.TraceID, "traceparent", "tracestate", "baggage", "Forwarded")
	for name := range result {
		if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			delete(result, name)
		}
	}
	return result
}

func filteredResponseHeaders(h http.Header) http.Header {
	result := withoutHopByHopHeaders(h)
	removeHeaders(result, "Set-Cookie", header.TraceID, "traceparent", "tracestate", "baggage")
	return result
}

func withoutHopByHopHeaders(h http.Header) http.Header {
	result := h.Clone()
	var connectionValues []string
	for name, values := range result {
		if strings.EqualFold(name, "Connection") {
			connectionValues = append(connectionValues, values...)
		}
	}
	removeHeaders(result, "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade")
	for _, value := range connectionValues {
		for name := range strings.SplitSeq(value, ",") {
			removeHeaders(result, strings.TrimSpace(name))
		}
	}
	return result
}

func removeHeaders(h http.Header, names ...string) {
	for _, name := range names {
		for existingName := range h {
			if strings.EqualFold(existingName, name) {
				delete(h, existingName)
			}
		}
	}
}
