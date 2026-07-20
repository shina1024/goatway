package gateway

import (
	"context"
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
	"goatway/internal/proxy"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/throttle"
)

const apiTokenHeader = "X-Goatway-API-Token"

func TestHandler_returns_route_decision_status_when_request_is_rejected(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, cfg *config.Config, request *http.Request)
		want    int
	}{
		{"no route", func(_ *testing.T, _ *config.Config, request *http.Request) { request.URL.Path = "/missing" }, http.StatusNotFound},
		{"missing token", func(_ *testing.T, _ *config.Config, _ *http.Request) {}, http.StatusUnauthorized},
		{"unknown token", func(_ *testing.T, _ *config.Config, request *http.Request) {
			request.Header.Set(apiTokenHeader, "unknown")
		}, http.StatusForbidden},
		{"client not allowed", func(_ *testing.T, _ *config.Config, request *http.Request) {
			request.Header.Set(apiTokenHeader, "staff-token")
		}, http.StatusForbidden},
		{"IP denied", func(_ *testing.T, _ *config.Config, request *http.Request) {
			request.Header.Set(apiTokenHeader, "public-token")
			request.RemoteAddr = "192.0.2.1:1234"
		}, http.StatusForbidden},
		{"invalid development request time", func(t *testing.T, _ *config.Config, request *http.Request) {
			t.Setenv("GOATWAY_ENV", "dev")
			request.Header.Set("X-Goatway-Request-Time", "not-rfc3339")
		}, http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			cfg := testConfig(t, "127.0.0.1", 1)
			request := httptest.NewRequest(http.MethodGet, "/products/42", nil)
			test.prepare(t, cfg, request)
			handler := newTestHandler(t, cfg, "public: 1\n")
			recorder := httptest.NewRecorder()

			// When
			handler.ServeHTTP(recorder, request)

			// Then
			require.Equal(t, test.want, recorder.Code)
		})
	}
}

func TestHandler_forwards_rewritten_request_when_route_is_allowed(t *testing.T) {
	// Given
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/catalog/42", request.URL.Path)
		require.NotEmpty(t, request.Header.Get("X-Goatway-Trace-ID"))
		writer.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	host, port := targetAddress(t, upstream.URL)
	cfg := testConfig(t, host, port)
	handler := newTestHandler(t, cfg, "public: 1\n")
	request := httptest.NewRequest(http.MethodGet, "/products/42", nil)
	request.Header.Set(apiTokenHeader, "public-token")
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()

	// When
	handler.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusCreated, recorder.Code)
}

type statusRecorder struct {
	header   http.Header
	statuses []int
}

func (recorder *statusRecorder) Header() http.Header { return recorder.header }

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.statuses = append(recorder.statuses, status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) { return len(body), nil }

func TestHandler_writes_single_response_when_upstream_times_out(t *testing.T) {
	// Given
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	cfg := testConfig(t, host, port)
	groupConfig := cfg.TargetGroups["catalog"]
	target := groupConfig.Targets[0]
	target.ReadTimeout = 50
	groupConfig.Targets[0] = target
	cfg.TargetGroups["catalog"] = groupConfig
	handler := newTestHandler(t, cfg, "public: 1\n")

	// When
	writer := &statusRecorder{header: make(http.Header)}
	handler.ServeHTTP(writer, gatewayRequest())

	// Then
	require.Len(t, writer.statuses, 1)
	require.Equal(t, http.StatusGatewayTimeout, writer.statuses[0])
}

func newTestHandler(t *testing.T, cfg *config.Config, limits string) *Handler {
	t.Helper()
	registry, err := targetgroup.NewRegistry(cfg.TargetGroups)
	require.NoError(t, err)
	routes, err := router.New(*cfg)
	require.NoError(t, err)
	limiter, err := throttle.NewLimiter(writeFile(t, "max_concurrent_requests.yml", limits))
	require.NoError(t, err)
	return NewHandler(
		cfg, registry, routes, limiter,
		WithProxy(proxy.NewHandler()),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
}

func testConfig(t *testing.T, host string, port int) *config.Config {
	t.Helper()
	return &config.Config{
		TargetGroups: map[config.TargetGroupID]config.TargetGroupConfig{
			"catalog": {Targets: []config.TargetConfig{{Host: host, Port: port, Weight: 1}}, MaxTryCount: 1},
		},
		Routes: []config.RouteConfig{{
			From: config.RouteFromConfig{Path: "^/products/(.+)$", Clients: []config.ClientType{"public"}, IPRangeGroups: []string{"loopback"}},
			To:   config.RouteToConfig{Destinations: []config.DestinationConfig{{TargetGroup: "catalog", Path: "/catalog/$1", Weight: 1}}},
		}},
		APIClientTokens:       map[config.ClientType][]string{"public": {"public-token"}, "staff": {"staff-token"}},
		IPRangeGroups:         map[string][]string{"loopback": {"127.0.0.0/8"}},
		MaxConcurrentRequests: map[config.ClientType]int{"public": 1},
	}
}

func targetAddress(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port
}

func writeFile(t *testing.T, name string, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func setThrottleState(t *testing.T) {
	t.Helper()
	require.NoError(t, throttle.SetDepType())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	updated := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		throttle.Poll(ctx, throttleStateFetcher{updated: updated}, time.Nanosecond)
		close(done)
	}()

	select {
	case <-updated:
		cancel()
	case <-ctx.Done():
		t.Fatal("throttle poller did not fetch deployment state")
	}
	stoppedCtx, stoppedCancel := context.WithTimeout(context.Background(), time.Second)
	defer stoppedCancel()
	select {
	case <-done:
	case <-stoppedCtx.Done():
		t.Fatal("throttle poller did not stop")
	}
}

type throttleStateFetcher struct {
	updated chan<- struct{}
}

func (fetcher throttleStateFetcher) FetchInstanceCounts(context.Context) (throttle.InstanceCounts, error) {
	return throttle.InstanceCounts{Primary: 1, Canary: 1}, nil
}

func (fetcher throttleStateFetcher) FetchTrafficWeight(context.Context) (throttle.TrafficWeight, error) {
	select {
	case fetcher.updated <- struct{}{}:
	default:
	}
	return throttle.TrafficWeight{Primary: 100, Canary: 100}, nil
}
