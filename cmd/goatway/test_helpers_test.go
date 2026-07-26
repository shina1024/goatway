package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/telemetry"
	"goatway/internal/throttle"
)

type testRuntime struct {
	provider   trace.TracerProvider
	propagator propagation.TraceContext
	handler    func(http.Handler) http.Handler
	shutdown   func(context.Context) error
}

func (runtime *testRuntime) TracerProvider() trace.TracerProvider {
	return runtime.provider
}

func (runtime *testRuntime) MeterProvider() metric.MeterProvider {
	return noop.NewMeterProvider()
}

func (runtime *testRuntime) TraceContext() propagation.TraceContext {
	return runtime.propagator
}

func (runtime *testRuntime) HTTPHandler(next http.Handler) http.Handler {
	if runtime.handler == nil {
		return next
	}
	return runtime.handler(next)
}

func (runtime *testRuntime) Shutdown(ctx context.Context) error {
	if runtime.shutdown == nil {
		return nil
	}
	return runtime.shutdown(ctx)
}

type testServer struct {
	listen   func() error
	shutdown func(context.Context) error
}

func (server testServer) ListenAndServe() error {
	return server.listen()
}

func (server testServer) Shutdown(ctx context.Context) error {
	return server.shutdown(ctx)
}

func testSettings(configDir string) runSettings {
	return runSettings{
		configDir:  configDir,
		listenAddr: "unused",
		devMode:    true,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func testDependencies(runtime telemetryRuntime, server httpServer) runDependencies {
	dependencies := productionDependencies()
	dependencies.configFromEnv = func() (telemetry.Config, error) {
		return telemetry.Config{}, nil
	}
	dependencies.newRuntime = func(context.Context, telemetry.Config) (telemetryRuntime, error) {
		return runtime, nil
	}
	dependencies.newServer = func(runSettings, http.Handler) httpServer {
		return server
	}
	dependencies.poll = func(ctx context.Context, _ *throttle.DeploymentTracker, _ throttle.Fetcher) {
		<-ctx.Done()
	}
	return dependencies
}

func testConfigDir(t *testing.T, upstreamURL string) string {
	t.Helper()
	endpoint, err := url.Parse(upstreamURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(endpoint.Port())
	require.NoError(t, err)

	files := map[string]string{ //nolint:gosec // test fixture intentionally contains a non-secret API token
		"target_groups.yml":           fmt.Sprintf("catalog:\n  targets:\n    - host: %s\n      port: %d\n      weight: 1\n", endpoint.Hostname(), port),
		"routes.yml":                  "- from:\n    path: ^/items/(.+)$\n    clients:\n      - public\n    ip_range_groups:\n      - office\n  to:\n    destinations:\n      - target_group: catalog\n        path: /$1\n        weight: 1\n",
		"api_client_tokens.yml":       "public:\n  - token\n",
		"ip_range_groups.yml":         "office:\n  - 127.0.0.1/32\n",
		"max_concurrent_requests.yml": "public: 10\n",
		"deployment.yml":              "primary_pods: 1\ncanary_pods: 0\nprimary_weight: 100\ncanary_weight: 0\n",
	}
	dir := t.TempDir()
	for name, contents := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600)
		require.NoError(t, err)
	}
	return dir
}
