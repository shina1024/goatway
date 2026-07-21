package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestS8_rejects_over_limit_then_releases_completed_requests(t *testing.T) {
	// Given
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var entered atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if entered.Add(1) <= 2 {
			started <- struct{}{}
			<-release
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/throttle$", "[client1]", "[]", "      - target_group: sample\n        path: /throttle\n        weight: 1\n"),
		tokens:       "client1: [token]\n", ipRanges: "{}\n", limits: "client1: 2\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})
	results := make(chan int, 2)
	for range 2 {
		go func() { results <- requestStatus(t, server, http.MethodGet, "/throttle", "token") }()
	}
	receive(t, started, "first request did not reach backend")
	receive(t, started, "second request did not reach backend")

	// When
	overLimit := requestStatus(t, server, http.MethodGet, "/throttle", "token")
	close(release)
	first := receive(t, results, "first request did not complete")
	second := receive(t, results, "second request did not complete")
	later := requestStatus(t, server, http.MethodGet, "/throttle", "token")

	// Then
	require.Equal(t, http.StatusTooManyRequests, overLimit)
	require.Equal(t, http.StatusNoContent, first)
	require.Equal(t, http.StatusNoContent, second)
	require.Equal(t, http.StatusNoContent, later)
}

func TestS9_disables_throttling_when_deployment_state_is_zero(t *testing.T) {
	// Given
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/degraded$", "[client1]", "[]", "      - target_group: sample\n        path: /degraded\n        weight: 1\n"),
		tokens:       "client1: [token]\n", ipRanges: "{}\n", limits: "client1: 2\n", deployment: "primary_pods: 0\nprimary_weight: 0\n",
	})
	results := make(chan int, 3)
	for range 3 {
		go func() { results <- requestStatus(t, server, http.MethodGet, "/degraded", "token") }()
	}
	receive(t, started, "first request did not reach backend")
	receive(t, started, "second request did not reach backend")
	receive(t, started, "third request was throttled")

	// When
	close(release)
	statuses := []int{receive(t, results, "first request did not complete"), receive(t, results, "second request did not complete"), receive(t, results, "third request did not complete")}

	// Then
	require.Equal(t, []int{http.StatusNoContent, http.StatusNoContent, http.StatusNoContent}, statuses)
}

func TestS10_generates_distinct_trace_ids(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Goatway-Trace-ID", request.Header.Get("X-Goatway-Trace-ID"))
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	server := newGateway(t, fixture{
		targetGroups: groupYAML("sample", host, port, ""),
		routes:       routeYAML("^/trace$", "[]", "[]", "      - target_group: sample\n        path: /trace\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	first := requestTraceID(t, server, "/trace")
	second := requestTraceID(t, server, "/trace")

	// Then
	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
	require.NotEqual(t, first, second)
}
