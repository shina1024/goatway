package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"goatway/internal/config"

	"github.com/stretchr/testify/require"
)

func TestRouter_Route_matchesResolvedClientIP_whenIPRangeConstrainsRoute(t *testing.T) {
	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		xForwardedFor  string
		wantErr        error
	}{
		{
			name:          "denies spoofed allowed XFF without trusted proxies",
			remoteAddr:    "192.168.0.4:8080",
			xForwardedFor: "10.0.0.4",
			wantErr:       ErrIPNotAllowed,
		},
		{
			name:          "allows remote address despite spoofed denied XFF without trusted proxies",
			remoteAddr:    "10.0.0.4:8080",
			xForwardedFor: "192.168.0.4",
		},
		{
			name:           "ignores XFF from an untrusted direct peer",
			trustedProxies: []string{"192.168.0.0/24"},
			remoteAddr:     "10.0.0.4:8080",
			xForwardedFor:  "172.16.0.4",
		},
		{
			name:           "allows rightmost untrusted client behind trusted proxies",
			trustedProxies: []string{"192.168.0.0/24"},
			remoteAddr:     "192.168.0.10:8080",
			xForwardedFor:  "10.0.0.4, 192.168.0.11",
		},
		{
			name:           "denies disallowed client behind trusted proxy",
			trustedProxies: []string{"192.168.0.0/24"},
			remoteAddr:     "192.168.0.10:8080",
			xForwardedFor:  "172.16.0.4",
			wantErr:        ErrIPNotAllowed,
		},
		{
			name:           "denies malformed XFF behind trusted proxy",
			trustedProxies: []string{"192.168.0.0/24"},
			remoteAddr:     "192.168.0.10:8080",
			xForwardedFor:  "not-an-ip, 10.0.0.4",
			wantErr:        ErrIPNotAllowed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			router := newTestRouter(
				t,
				testRoute("^/sample$", nil, []string{"office"}, testDestinations()),
				withRanges(),
				func(configuration *config.Config) {
					configuration.Gateway.TrustedProxies = test.trustedProxies
				},
			)
			req := httptest.NewRequest(http.MethodGet, "/sample", nil)
			req.RemoteAddr = test.remoteAddr
			req.Header.Set("X-Forwarded-For", test.xForwardedFor)

			// When
			_, err := router.Route(req)

			// Then
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRouter_Route_fallsBackToTrustedRemoteAddr_whenXFFHasNoUntrustedHop(t *testing.T) {
	tests := []struct {
		name          string
		xForwardedFor string
	}{
		{name: "all XFF hops are trusted", xForwardedFor: "192.168.0.11, 192.168.0.12"},
		{name: "XFF is empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			router := newTestRouter(
				t,
				testRoute("^/sample$", nil, []string{"office"}, testDestinations()),
				func(configuration *config.Config) {
					configuration.IPRangeGroups = map[string][]string{"office": {"192.168.0.10/32"}}
					configuration.Gateway.TrustedProxies = []string{"192.168.0.0/24"}
				},
			)
			req := httptest.NewRequest(http.MethodGet, "/sample", nil)
			req.RemoteAddr = "192.168.0.10:8080"
			req.Header.Set("X-Forwarded-For", test.xForwardedFor)

			// When
			_, err := router.Route(req)

			// Then
			require.NoError(t, err)
		})
	}
}

func TestRouter_Route_ignoresClientIPResolution_whenRouteHasNoIPRangeConstraint(t *testing.T) {
	// Given
	router := newTestRouter(
		t,
		testRoute("^/sample$", nil, nil, testDestinations()),
		func(configuration *config.Config) {
			configuration.Gateway.TrustedProxies = []string{"192.168.0.0/24"}
		},
	)
	req := httptest.NewRequest(http.MethodGet, "/sample", nil)
	req.RemoteAddr = "192.168.0.10:8080"
	req.Header.Set("X-Forwarded-For", "not-an-ip")

	// When
	_, err := router.Route(req)

	// Then
	require.NoError(t, err)
}
