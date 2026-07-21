package e2e_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheme_explicitHTTPSchemeForwardsSuccessfully(t *testing.T) {
	// Given
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	groups := fmt.Sprintf("api:\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n      scheme: http\n", host, port)
	routes := "- from:\n    path: ^/test$\n    clients: []\n    ip_range_groups: []\n  to:\n    destinations:\n      - target_group: api\n        path: /\n        weight: 1\n"
	server := newGateway(t, fixture{
		targetGroups: groups,
		routes:       routes,
		tokens:       "{}\n",
		ipRanges:     "{}\n",
		limits:       "{}\n",
		deployment:   "primary_pods: 1\ncanary_pods: 0\nprimary_weight: 100\ncanary_weight: 0\n",
	})

	// When
	status := requestStatus(t, server, http.MethodGet, "/test", "")

	// Then
	require.Equal(t, http.StatusNoContent, status)
}
