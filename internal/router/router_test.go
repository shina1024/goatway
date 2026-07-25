package router

import (
	"net/http/httptest"
	"testing"
	"time"

	"goatway/internal/config"

	"github.com/stretchr/testify/require"
)

func TestRouter_Route_rewritesAllDestinations_whenRouteMatches(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample/(.+)$", nil, nil, []config.DestinationConfig{
		{TargetGroup: "primary", Path: "/$1", Weight: 1},
		{TargetGroup: "canary", Path: "/canary/$1", Weight: 1},
	}))
	req := httptest.NewRequest("GET", "/sample/hoge", nil)

	// When
	match, err := router.Route(req)

	// Then
	require.NoError(t, err)
	require.Equal(t, "primary", match.TargetGroupID)
	require.Equal(t, map[string]string{"primary": "/hoge", "canary": "/canary/hoge"}, match.RoutedPathMap)
}

func TestRouter_Route_returnsNoRoute_whenNoPathMatches(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample/(.+)$", nil, nil, testDestinations()))
	req := httptest.NewRequest("GET", "/other/hoge", nil)

	// When
	_, err := router.Route(req)

	// Then
	require.ErrorIs(t, err, ErrNoRoute)
}

func TestRouter_Route_returnsFirstPathMatchAuthError_whenLaterRouteWouldMatch(t *testing.T) {
	// Given
	configuration := config.Config{Routes: []config.RouteConfig{
		testRoute("^/sample$", []config.ClientType{"public"}, nil, testDestinations()),
		testRoute("^/sample$", nil, nil, testDestinations()),
	}}
	router, err := New(configuration)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/sample", nil)

	// When
	_, err = router.Route(req)

	// Then
	require.ErrorIs(t, err, ErrMissingToken)
}

func TestRouter_Route_usesConfiguredWeights_whenDestinationsDiffer(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample$", nil, nil, []config.DestinationConfig{
		{TargetGroup: "primary", Path: "/", Weight: 2},
		{TargetGroup: "canary", Path: "/", Weight: 1},
	}))
	req := httptest.NewRequest("GET", "/sample", nil)

	// When
	picks := make(map[string]int)
	for range 9 {
		match, err := router.Route(req)
		require.NoError(t, err)
		picks[match.TargetGroupID]++
	}

	// Then
	require.Equal(t, map[string]int{"primary": 6, "canary": 3}, picks)
}

func TestRouter_Route_returnsAuthErrors_whenRouteRequiresClient(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample$", []config.ClientType{"public"}, nil, testDestinations()), withTokens())
	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{name: "missing token", wantErr: ErrMissingToken},
		{name: "unknown token", token: "unknown", wantErr: ErrUnknownToken},
		{name: "client is not allowed", token: "backoffice-token", wantErr: ErrClientNotAllowed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			req := httptest.NewRequest("GET", "/sample", nil)
			req.Header.Set("X-Goatway-API-Token", test.token)

			// When
			_, err := router.Route(req)

			// Then
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestRouter_Route_returnsEmptyClient_whenRouteHasNoClientConstraint(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample$", nil, nil, testDestinations()))
	req := httptest.NewRequest("GET", "/sample", nil)

	// When
	match, err := router.Route(req)

	// Then
	require.NoError(t, err)
	require.Empty(t, match.ClientType)
}

func TestRouter_Route_enforcesIPRanges_whenRouteRequiresThem(t *testing.T) {
	// Given
	router := newTestRouter(t, testRoute("^/sample$", nil, []string{"office"}, testDestinations()), withRanges())
	tests := []struct {
		name    string
		remote  string
		wantErr error
	}{
		{name: "IP with port is allowed", remote: "10.0.0.4:8080"},
		{name: "raw IP is allowed", remote: "10.0.0.5"},
		{name: "outside range is denied", remote: "192.168.0.4:8080", wantErr: ErrIPNotAllowed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			req := httptest.NewRequest("GET", "/sample", nil)
			req.RemoteAddr = test.remote

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

func TestRouter_Route_deniesIP_whenConfiguredRangeGroupsAreEmpty(t *testing.T) {
	// Given
	configuration := config.Config{
		Routes:        []config.RouteConfig{testRoute("^/sample$", nil, []string{"office"}, testDestinations())},
		IPRangeGroups: map[string][]string{"office": {}},
	}
	router, err := New(configuration)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/sample", nil)

	// When
	_, err = router.Route(req)

	// Then
	require.ErrorIs(t, err, ErrIPNotAllowed)
}

func TestWithRequestTimeOverride_usesHeaderOnlyInDevelopment(t *testing.T) {
	validTime := time.Date(2026, time.July, 20, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name     string
		devMode  bool
		header   string
		wantTime bool
		wantErr  error
	}{
		{name: "development valid header", devMode: true, header: validTime.Format(time.RFC3339), wantTime: true},
		{name: "development invalid header", devMode: true, header: "invalid", wantErr: ErrInvalidRequestTime},
		{name: "production ignores header", devMode: false, header: validTime.Format(time.RFC3339)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			req := httptest.NewRequest("GET", "/sample", nil)
			req.Header.Set("X-Goatway-Request-Time", test.header)

			// When
			updated, err := WithRequestTimeOverride(req, test.devMode)

			// Then
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			got, ok := RequestTime(updated.Context())
			require.Equal(t, test.wantTime, ok)
			if test.wantTime {
				require.Equal(t, validTime, got)
			}
		})
	}
}

type configOption func(*config.Config)

func newTestRouter(t *testing.T, route config.RouteConfig, options ...configOption) *Router {
	t.Helper()
	configuration := config.Config{Routes: []config.RouteConfig{route}}
	for _, option := range options {
		option(&configuration)
	}
	router, err := New(configuration)
	require.NoError(t, err)
	return router
}

func testRoute(path string, clients []config.ClientType, groups []string, destinations []config.DestinationConfig) config.RouteConfig {
	return config.RouteConfig{
		From: config.RouteFromConfig{Path: path, Clients: clients, IPRangeGroups: groups},
		To:   config.RouteToConfig{Destinations: destinations},
	}
}

func testDestinations() []config.DestinationConfig {
	return []config.DestinationConfig{{TargetGroup: "primary", Path: "/", Weight: 1}}
}

func withTokens() configOption {
	return func(configuration *config.Config) {
		configuration.APIClientTokens = map[config.ClientType][]string{
			"public":     {"public-token"},
			"backoffice": {"backoffice-token"},
		}
	}
}

func withRanges() configOption {
	return func(configuration *config.Config) {
		configuration.IPRangeGroups = map[string][]string{"office": {"10.0.0.0/24"}}
	}
}
