package e2e_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestS5_retries_server_errors_only_for_idempotent_requests(t *testing.T) {
	// Given
	setThrottleState(t, defaultState())
	var failed, healthy atomic.Int64
	backendFail := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		failed.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer backendFail.Close()
	backendHealthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		healthy.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendHealthy.Close()
	hostFail, portFail := backendAddress(t, backendFail)
	hostHealthy, portHealthy := backendAddress(t, backendHealthy)
	value := fixture{
		targetGroups: fmt.Sprintf("sample:\n  max_try_count: 2\n  retry_cases: [server_error]\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n    - host: %s\n      port: %d\n      weight: 1\n", hostFail, portFail, hostHealthy, portHealthy),
		routes:       routeYAML("^/retry$", "[]", "[]", "      - target_group: sample\n        path: /retry\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	}
	getServer := newGateway(t, value)
	postServer := newGateway(t, value)

	// When
	getStatus := requestStatus(t, getServer, http.MethodGet, "/retry", "")
	postStatus := requestStatus(t, postServer, http.MethodPost, "/retry", "")

	// Then
	require.Equal(t, http.StatusOK, getStatus)
	require.Equal(t, http.StatusInternalServerError, postStatus)
	require.Equal(t, int64(2), failed.Load())
	require.Equal(t, int64(1), healthy.Load())
}

func TestS6_retries_timeouts_and_returns_gateway_timeout_when_unmatched(t *testing.T) {
	// Given
	setThrottleState(t, defaultState())
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer slow.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) }))
	defer healthy.Close()
	slowHost, slowPort := backendAddress(t, slow)
	healthyHost, healthyPort := backendAddress(t, healthy)
	retryServer := newGateway(t, fixture{
		targetGroups: fmt.Sprintf("retry:\n  max_try_count: 2\n  read_timeout: 100\n  retry_cases: [timeout]\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n    - host: %s\n      port: %d\n      weight: 1\n", slowHost, slowPort, healthyHost, healthyPort),
		routes:       routeYAML("^/retry-timeout$", "[]", "[]", "      - target_group: retry\n        path: /retry-timeout\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})
	singleServer := newGateway(t, fixture{
		targetGroups: groupYAML("single", slowHost, slowPort, "  read_timeout: 100\n"),
		routes:       routeYAML("^/single-timeout$", "[]", "[]", "      - target_group: single\n        path: /single-timeout\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	retryStatus := requestStatus(t, retryServer, http.MethodGet, "/retry-timeout", "")
	singleStatus := requestStatus(t, singleServer, http.MethodGet, "/single-timeout", "")

	// Then
	require.Equal(t, http.StatusOK, retryStatus)
	require.Equal(t, http.StatusGatewayTimeout, singleStatus)
}

func TestS7_retries_to_configured_target_group_with_its_rewrite(t *testing.T) {
	// Given
	setThrottleState(t, defaultState())
	backendA := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusInternalServerError) }))
	defer backendA.Close()
	paths := make(chan string, 1)
	backendB := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths <- request.URL.Path
		writer.WriteHeader(http.StatusOK)
	}))
	defer backendB.Close()
	hostA, portA := backendAddress(t, backendA)
	hostB, portB := backendAddress(t, backendB)
	server := newGateway(t, fixture{
		targetGroups: fmt.Sprintf("TargetGroupA:\n  max_try_count: 2\n  retry_cases: [server_error]\n  retry_to_target_group_id: TargetGroupB\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\nTargetGroupB:\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n", hostA, portA, hostB, portB),
		routes:       routeYAML("^/cross$", "[]", "[]", "      - target_group: TargetGroupA\n        path: /from-a\n        weight: 1\n      - target_group: TargetGroupB\n        path: /from-b\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	status := requestStatus(t, server, http.MethodGet, "/cross", "")

	// Then
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "/from-b", receive(t, paths, "retry target did not receive a request"))
}
