package proxy

import (
	"net/http"
	"strings"

	"goatway/internal/headers"
)

func copyForwardableHeaders(destination, source http.Header) {
	for name, values := range source {
		destination[name] = append([]string(nil), values...)
	}
}

func filteredRequestHeaders(header http.Header) http.Header {
	result := withoutHopByHopHeaders(header)
	removeHeaders(result, headers.APIToken, "Authorization", "Cookie", headers.RequestTime, headers.TraceID, "traceparent", "tracestate", "baggage", "Forwarded")
	for name := range result {
		if strings.HasPrefix(strings.ToLower(name), "x-forwarded-") {
			delete(result, name)
		}
	}
	return result
}

func filteredResponseHeaders(header http.Header) http.Header {
	result := withoutHopByHopHeaders(header)
	removeHeaders(result, "Set-Cookie", headers.TraceID, "traceparent", "tracestate", "baggage")
	return result
}

func withoutHopByHopHeaders(header http.Header) http.Header {
	result := header.Clone()
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

func removeHeaders(header http.Header, names ...string) {
	for _, name := range names {
		for existingName := range header {
			if strings.EqualFold(existingName, name) {
				delete(header, existingName)
			}
		}
	}
}
