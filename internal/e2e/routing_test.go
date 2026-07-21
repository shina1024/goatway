package e2e_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestS1_routes_and_rewrites_matching_requests(t *testing.T) {
	// Given
	paths := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths <- request.URL.Path
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/sample/(.+)$", "[]", "[]", "      - target_group: sample\n        path: /$1\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	status := requestStatus(t, server, http.MethodGet, "/sample/hoge", "")

	// Then
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "/hoge", receive(t, paths, "backend did not receive rewritten request"))
}

func TestS1_returns_not_found_when_no_route_matches(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/sample/(.+)$", "[]", "[]", "      - target_group: sample\n        path: /$1\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	status := requestStatus(t, server, http.MethodGet, "/nomatch", "")

	// Then
	require.Equal(t, http.StatusNotFound, status)
}

func TestS2_enforces_tokens_and_route_clients(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "valid route client passes", token: "abcde12345", want: http.StatusNoContent},
		{name: "missing token is unauthorized", want: http.StatusUnauthorized},
		{name: "unknown token is forbidden", token: "unknown", want: http.StatusForbidden},
		{name: "other client token is forbidden", token: "other-token", want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
			defer backend.Close()
			host, port := backendAddress(t, backend)
			server := newGateway(t, fixture{
				targetGroups: groupYAML("sample", host, port, ""),
				routes:       routeYAML("^/token$", "[SampleClient]", "[]", "      - target_group: sample\n        path: /token\n        weight: 1\n"),
				tokens:       "SampleClient: [abcde12345]\nOtherClient: [other-token]\n",
				ipRanges:     "{}\n", limits: "SampleClient: 10\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
			})

			// When
			status := requestStatus(t, server, http.MethodGet, "/token", test.token)

			// Then
			require.Equal(t, test.want, status)
		})
	}
}

func TestS3_enforces_ip_range_groups(t *testing.T) {
	for _, test := range []struct {
		name   string
		ranges string
		want   int
	}{
		{name: "loopback passes", ranges: "allowed: [127.0.0.1/32]\n", want: http.StatusNoContent},
		{name: "non loopback range is forbidden", ranges: "allowed: [10.0.0.0/8]\n", want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
			defer backend.Close()
			host, port := backendAddress(t, backend)
			server := newGateway(t, fixture{
				targetGroups: groupYAML("sample", host, port, ""),
				routes:       routeYAML("^/ip$", "[]", "[allowed]", "      - target_group: sample\n        path: /ip\n        weight: 1\n"),
				tokens:       "{}\n", ipRanges: test.ranges, limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
			})

			// When
			status := requestStatus(t, server, http.MethodGet, "/ip", "")

			// Then
			require.Equal(t, test.want, status)
		})
	}
}

func TestS4_distributes_weighted_targets_and_interleaves_smaller_weight(t *testing.T) {
	// Given
	var hitsA, hitsB atomic.Int64
	var orderMu sync.Mutex
	var order []string
	backendA := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hitsA.Add(1)
		orderMu.Lock()
		order = append(order, "A")
		orderMu.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hitsB.Add(1)
		orderMu.Lock()
		order = append(order, "B")
		orderMu.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backendB.Close()
	hostA, portA := backendAddress(t, backendA)
	hostB, portB := backendAddress(t, backendB)
	server := newGateway(t, fixture{
		targetGroups: "sample:\n  targets:\n    - host: " + hostA + "\n      port: " + fmt.Sprint(portA) + "\n      weight: 4\n    - host: " + hostB + "\n      port: " + fmt.Sprint(portB) + "\n      weight: 1\n",
		routes:       routeYAML("^/weighted$", "[]", "[]", "      - target_group: sample\n        path: /weighted\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	for range 10 {
		require.Equal(t, http.StatusNoContent, requestStatus(t, server, http.MethodGet, "/weighted", ""))
	}

	// Then
	require.Equal(t, int64(8), hitsA.Load())
	require.Equal(t, int64(2), hitsB.Load())
	orderMu.Lock()
	require.NotEqual(t, []string{"A", "A", "A", "A", "A", "A", "A", "A", "B", "B"}, order)
	orderMu.Unlock()
}
