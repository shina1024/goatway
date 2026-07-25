package proxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/router"
)

func TestHandler_Forward_rebuilds_forwarding_headers_from_direct_connection(t *testing.T) {
	// Given
	upstreamHeaders := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.42:45678"
	request.Host = "shop.example.test:8443"
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Forwarded", "for=192.0.2.1;host=attacker.example")
	request.Header.Set("X-Forwarded-For", "192.0.2.2")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "ftp")
	request.Header.Set("X-Forwarded-Port", "21")
	request.Header.Set("X-Forwarded-Server", "attacker")

	// When
	_, err := NewHandler().Forward(httptest.NewRecorder(), request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
	})

	// Then
	require.NoError(t, err)
	got := <-upstreamHeaders
	require.Equal(t, []string{"198.51.100.42"}, got.Values("X-Forwarded-For"))
	require.Equal(t, "shop.example.test:8443", got.Get("X-Forwarded-Host"))
	require.Equal(t, "https", got.Get("X-Forwarded-Proto"))
	require.Empty(t, got.Get("Forwarded"))
	require.Empty(t, got.Get("X-Forwarded-Port"))
	require.Empty(t, got.Get("X-Forwarded-Server"))
}

func TestHandler_Forward_omits_forwarded_for_when_remote_address_is_invalid(t *testing.T) {
	// Given
	upstreamHeaders := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamHeaders <- request.Header.Clone()
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "not-a-host-port"
	request.Host = "shop.example.test"

	// When
	_, err := NewHandler().Forward(httptest.NewRecorder(), request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
	})

	// Then
	require.NoError(t, err)
	got := <-upstreamHeaders
	require.Empty(t, got.Values("X-Forwarded-For"))
	require.Equal(t, "shop.example.test", got.Get("X-Forwarded-Host"))
	require.Equal(t, "http", got.Get("X-Forwarded-Proto"))
}

func TestHandler_ForwardWithRetry_rebuilds_forwarded_for_for_each_attempt(t *testing.T) {
	// Given
	firstXFF := make(chan string, 1)
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		firstXFF <- request.Header.Get("X-Forwarded-For")
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer first.Close()
	secondXFF := make(chan string, 1)
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		secondXFF <- request.Header.Get("X-Forwarded-For")
		writer.WriteHeader(http.StatusOK)
	}))
	defer second.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			Targets:           []config.TargetConfig{retryTarget(t, first.URL, time.Second), retryTarget(t, second.URL, time.Second)},
			MaxTryCount:       2,
			RetryCases:        []string{"server_error"},
			RetryBaseInterval: 1,
		},
	})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.21:9876"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")

	// When
	result, err := NewHandler(WithRetrySleeper(func(time.Duration) {})).ForwardWithRetry(
		httptest.NewRecorder(),
		request,
		retryInput(group, map[string]string{"api": "/"}),
	)

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, "203.0.113.21", <-firstXFF)
	require.Equal(t, "203.0.113.21", <-secondXFF)
}
