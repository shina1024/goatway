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
	"goatway/internal/headers"
	"goatway/internal/proxy"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/telemetry"
	"goatway/internal/throttle"
)

type fixture struct {
	targetGroups string
	routes       string
	tokens       string
	ipRanges     string
	limits       string
	deployment   string
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
	tracker := throttle.NewDeploymentTracker()
	require.NoError(t, tracker.SetDepType())
	runtime, err := telemetry.New(t.Context(), telemetry.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	updated := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		tracker.Poll(ctx, notifyingFetcher{
			fetcher: throttle.NewFileFetcher(filepath.Join(dir, "deployment.yml")),
			updated: updated,
		}, time.Nanosecond)
		close(done)
	}()
	receive(t, updated, "throttle poller did not update state")
	cancel()
	receive(t, done, "throttle poller did not stop")
	handler := gateway.NewHandler(
		cfg, registry, routes, limiter, tracker,
		gateway.WithProxy(proxy.NewHandler(proxy.WithTelemetry(runtime.TracerProvider(), runtime.TraceContext()))),
		gateway.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	server := httptest.NewServer(runtime.HTTPHandler(handler))
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

type notifyingFetcher struct {
	fetcher throttle.Fetcher
	updated chan<- struct{}
}

func (fetcher notifyingFetcher) FetchInstanceCounts(ctx context.Context) (throttle.InstanceCounts, error) {
	return fetcher.fetcher.FetchInstanceCounts(ctx)
}

func (fetcher notifyingFetcher) FetchTrafficWeight(ctx context.Context) (throttle.TrafficWeight, error) {
	weight, err := fetcher.fetcher.FetchTrafficWeight(ctx)
	if err != nil {
		return throttle.TrafficWeight{}, err
	}
	select {
	case fetcher.updated <- struct{}{}:
	default:
	}
	return weight, nil
}

func requestStatus(t *testing.T, server *httptest.Server, method string, path string, token string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
	require.NoError(t, err)
	if token != "" {
		request.Header.Set(headers.APIToken, token)
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
	return response.Header.Get(headers.TraceID)
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
