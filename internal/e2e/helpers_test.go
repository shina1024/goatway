package e2e_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/gateway"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/throttle"
)

const tokenHeader = "X-Goatway-API-Token"

type fixture struct {
	targetGroups string
	routes       string
	tokens       string
	ipRanges     string
	limits       string
	deployment   string
}

type deploymentState struct {
	primaryPods   int
	canaryPods    int
	primaryWeight int
	canaryWeight  int
}

func defaultState() deploymentState {
	return deploymentState{primaryPods: 1, primaryWeight: 100}
}

func newGateway(t *testing.T, value fixture) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	writeYAML(t, dir, "target_groups.yml", value.targetGroups)
	writeYAML(t, dir, "routes.yml", value.routes)
	writeYAML(t, dir, "api_client_tokens.yml", value.tokens)
	writeYAML(t, dir, "ip_range_groups.yml", value.ipRanges)
	writeYAML(t, dir, "max_concurrent_requests.yml", value.limits)
	writeYAML(t, dir, "deployment.yml", value.deployment)

	cfg, err := config.Load(dir)
	require.NoError(t, err)
	registry, err := targetgroup.NewRegistry(cfg.TargetGroups)
	require.NoError(t, err)
	routes, err := router.New(*cfg)
	require.NoError(t, err)
	limiter, err := throttle.NewLimiter(filepath.Join(dir, "max_concurrent_requests.yml"))
	require.NoError(t, err)
	handler := gateway.NewHandler(cfg, registry, routes, limiter,
		gateway.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func writeYAML(t *testing.T, dir string, name string, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600))
}

func backendAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port
}

func groupYAML(id string, host string, port int, extra string) string {
	return fmt.Sprintf("%s:\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n%s", id, host, port, extra)
}

func routeYAML(path string, clients string, ranges string, destinations string) string {
	return fmt.Sprintf("- from:\n    path: %s\n    clients: %s\n    ip_range_groups: %s\n  to:\n    destinations:\n%s", path, clients, ranges, destinations)
}

func setThrottleState(t *testing.T, state deploymentState) {
	t.Helper()
	require.NoError(t, throttle.SetDepType())
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	updated := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		throttle.Poll(ctx, stateFetcher{state: state, updated: updated}, time.Nanosecond)
		close(done)
	}()
	receive(t, updated, "throttle poller did not update state")
	cancel()
	receive(t, done, "throttle poller did not stop")
}

type stateFetcher struct {
	state   deploymentState
	updated chan<- struct{}
}

func (fetcher stateFetcher) FetchInstanceCounts(context.Context) (throttle.InstanceCounts, error) {
	return throttle.InstanceCounts{Primary: fetcher.state.primaryPods, Canary: fetcher.state.canaryPods}, nil
}

func (fetcher stateFetcher) FetchTrafficWeight(context.Context) (throttle.TrafficWeight, error) {
	select {
	case fetcher.updated <- struct{}{}:
	default:
	}
	return throttle.TrafficWeight{Primary: fetcher.state.primaryWeight, Canary: fetcher.state.canaryWeight}, nil
}

func requestStatus(t *testing.T, server *httptest.Server, method string, path string, token string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
	require.NoError(t, err)
	if token != "" {
		request.Header.Set(tokenHeader, token)
	}
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	return response.StatusCode
}

func requestTraceID(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	return response.Header.Get("X-Goatway-Trace-ID")
}

func receive[T any](t *testing.T, channel <-chan T, message string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal(message)
		var zero T
		return zero
	}
}
