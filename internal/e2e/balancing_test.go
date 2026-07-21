package e2e_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestS4_alternates_equal_weight_targets(t *testing.T) {
	// Given
	var mutex sync.Mutex
	var order []string
	backend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			mutex.Lock()
			order = append(order, name)
			mutex.Unlock()
			writer.WriteHeader(http.StatusNoContent)
		}))
	}
	backendA := backend("A")
	defer backendA.Close()
	backendB := backend("B")
	defer backendB.Close()
	hostA, portA := backendAddress(t, backendA)
	hostB, portB := backendAddress(t, backendB)
	server := newGateway(t, fixture{
		targetGroups: fmt.Sprintf("sample:\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n    - host: %s\n      port: %d\n      weight: 1\n", hostA, portA, hostB, portB),
		routes:       routeYAML("^/equal$", "[]", "[]", "      - target_group: sample\n        path: /equal\n        weight: 1\n"),
		tokens:       "{}\n", ipRanges: "{}\n", limits: "{}\n", deployment: "primary_pods: 1\nprimary_weight: 100\n",
	})

	// When
	for range 6 {
		require.Equal(t, http.StatusNoContent, requestStatus(t, server, http.MethodGet, "/equal", ""))
	}

	// Then
	mutex.Lock()
	require.Equal(t, []string{"A", "B", "A", "B", "A", "B"}, order)
	mutex.Unlock()
}
