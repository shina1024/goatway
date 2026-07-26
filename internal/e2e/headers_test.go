package e2e_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/headers"
)

func TestIntegratedGateway_enforces_trace_and_header_trust_boundaries(t *testing.T) {
	// Given
	const (
		traceID       = "4bf92f3577b34da6a3ce929d0e0e4736"
		parentSpanID  = "00f067aa0ba902b7"
		traceparent   = "00-" + traceID + "-" + parentSpanID + "-01"
		tracestate    = "vendor=client"
		gatewayHost   = "gateway.example.test:8080"
		clientToken   = "valid-client-token"
		spoofedTrace  = "client-spoofed-trace"
		backendTrace  = "backend-spoofed-trace"
		backendParent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	)
	upstreamHeaders := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.Header().Set(headers.TraceID, backendTrace)
		writer.Header().Set("traceparent", backendParent)
		writer.Header().Set("tracestate", "vendor=backend")
		writer.Header().Set("baggage", "tenant=backend")
		writer.Header().Add("Set-Cookie", "session=backend")
		writer.Header().Add("Set-Cookie", "preference=backend")
		writer.Header().Set("X-Backend-Safe", "forwarded")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/protected$", "[SampleClient]", "[]", "      - target_group: sample\n        path: /protected\n        weight: 1\n"),
		tokens:       "SampleClient: [" + clientToken + "]\n",
		ipRanges:     "{}\n",
		limits:       "SampleClient: 10\n",
		deployment:   "primary_pods: 1\nprimary_weight: 100\n",
	})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/protected", nil)
	require.NoError(t, err)
	request.Host = gatewayHost
	request.Header.Set(headers.APIToken, clientToken)
	request.Header.Set(headers.TraceID, spoofedTrace)
	request.Header.Set("Authorization", "Bearer client-spoof")
	request.Header.Set("Cookie", "session=client-spoof")
	request.Header.Set(headers.RequestTime, "2030-01-02T03:04:05Z")
	request.Header.Set("traceparent", traceparent)
	request.Header.Set("tracestate", tracestate)
	request.Header.Set("baggage", "tenant=client-spoof")
	request.Header.Set("Forwarded", "for=192.0.2.1;host=attacker.example")
	request.Header.Set("X-Forwarded-For", "192.0.2.2")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "ftp")
	request.Header.Set("X-Forwarded-Port", "21")
	request.Header.Set("X-Forwarded-Server", "attacker")
	request.Header.Set("X-Forwarded-By", "attacker")

	// When
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()

	// Then
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.Equal(t, traceID, response.Header.Get(headers.TraceID))
	require.Equal(t, "forwarded", response.Header.Get("X-Backend-Safe"))
	require.Empty(t, response.Header.Get("traceparent"))
	require.Empty(t, response.Header.Get("tracestate"))
	require.Empty(t, response.Header.Get("baggage"))
	require.Empty(t, response.Header.Values("Set-Cookie"))
	require.Empty(t, response.Cookies())

	got := receive(t, upstreamHeaders, "backend did not receive the protected request")
	require.Equal(t, traceID, got.Get(headers.TraceID))
	propagated := propagation.TraceContext{}.Extract(t.Context(), propagation.HeaderCarrier(got))
	spanContext := trace.SpanContextFromContext(propagated)
	require.True(t, spanContext.IsValid())
	require.Equal(t, traceID, spanContext.TraceID().String())
	require.NotEqual(t, parentSpanID, spanContext.SpanID().String())
	require.True(t, spanContext.TraceFlags().IsSampled())
	require.Equal(t, tracestate, spanContext.TraceState().String())

	for _, name := range []string{
		headers.APIToken,
		"Authorization",
		"Cookie",
		headers.RequestTime,
		"baggage",
		"Forwarded",
	} {
		require.Empty(t, got.Get(name), name)
	}
	directIP := net.ParseIP(strings.TrimSpace(got.Get("X-Forwarded-For")))
	require.NotNil(t, directIP)
	require.True(t, directIP.IsLoopback())
	require.Equal(t, gatewayHost, got.Get("X-Forwarded-Host"))
	require.Equal(t, "http", got.Get("X-Forwarded-Proto"))
	for _, name := range []string{"X-Forwarded-Port", "X-Forwarded-Server", "X-Forwarded-By"} {
		require.Empty(t, got.Get(name), name)
	}
}
