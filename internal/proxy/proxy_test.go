package proxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
)

func TestHandler_Forward_forwards_rewritten_request_and_copies_response(t *testing.T) {
	// Given
	var gotPath, gotQuery, gotHeader, gotTrace, gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		gotPath, gotQuery = request.URL.Path, request.URL.RawQuery
		gotHeader, gotTrace, gotBody = request.Header.Get("X-Client-Header"), request.Header.Get(traceHeader), string(body)
		require.Empty(t, request.Header.Get("X-Hop"))
		writer.Header().Set("X-Upstream-Header", "copied")
		writer.Header().Set("Connection", "X-Upstream-Hop")
		writer.Header().Set("X-Upstream-Hop", "removed")
		writer.WriteHeader(http.StatusBadGateway)
		_, err = writer.Write([]byte("upstream body"))
		require.NoError(t, err)
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, 100*time.Millisecond)
	request := httptest.NewRequest(http.MethodPost, "/incoming?keep=this", strings.NewReader("request body"))
	request.Header.Set("X-Client-Header", "forwarded")
	request.Header.Set("Connection", "X-Hop")
	request.Header.Set("X-Hop", "removed")
	recorder := httptest.NewRecorder()
	handler := NewHandler()

	// When
	result, err := handler.Forward(recorder, request, ForwardInput{
		Target: target,
		Group:  group,
		Match:  router.Match{TargetGroupID: "api", RoutedPathMap: map[string]string{"api": "/rewritten"}},
	})

	// Then
	require.NoError(t, err)
	require.Equal(t, ErrClassNone, result.ErrClass)
	require.Equal(t, http.StatusBadGateway, result.StatusCode)
	require.NotEmpty(t, result.TraceID)
	require.Equal(t, "/rewritten", gotPath)
	require.Equal(t, "keep=this", gotQuery)
	require.Equal(t, "forwarded", gotHeader)
	require.Equal(t, result.TraceID, gotTrace)
	require.Equal(t, "request body", gotBody)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Equal(t, "copied", recorder.Header().Get("X-Upstream-Header"))
	require.Empty(t, recorder.Header().Get("Connection"))
	require.Empty(t, recorder.Header().Get("X-Upstream-Hop"))
	require.Equal(t, "upstream body", recorder.Body.String())
}

func TestHandler_Forward_classifies_timeout_and_transport_errors(t *testing.T) {
	// Given
	slowBackend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer slowBackend.Close()
	tests := []struct {
		name      string
		address   string
		readDelay time.Duration
		wantClass ErrClass
	}{
		{name: "read timeout", address: slowBackend.URL, readDelay: 20 * time.Millisecond, wantClass: ErrClassTimeout},
		{name: "unreachable port", address: "http://127.0.0.1:1", readDelay: time.Second, wantClass: ErrClassOther},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group, target := testTarget(t, test.address, test.readDelay)
			recorder := httptest.NewRecorder()

			// When
			result, err := NewHandler().Forward(recorder, httptest.NewRequest(http.MethodGet, "/", nil), ForwardInput{
				Target: target,
				Group:  group,
				Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
			})

			// Then
			require.Error(t, err)
			require.Equal(t, test.wantClass, result.ErrClass)
		})
	}
}

func TestHandler_Forward_logs_460_when_client_cancels(t *testing.T) {
	// Given
	started := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer backend.Close()
	group, target := testTarget(t, backend.URL, time.Second)
	var logs bytes.Buffer
	handler := NewHandler(WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestContext)
	resultCh := make(chan AttemptResult, 1)
	errCh := make(chan error, 1)

	// When
	go func() {
		result, err := handler.Forward(httptest.NewRecorder(), request, ForwardInput{
			Target: target,
			Group:  group,
			Match:  router.Match{RoutedPathMap: map[string]string{"api": "/"}},
		})
		resultCh <- result
		errCh <- err
	}()
	<-started
	cancel()
	result, err := <-resultCh, <-errCh

	// Then
	require.Error(t, err)
	require.Equal(t, ErrClassOther, result.ErrClass)
	require.Contains(t, logs.String(), `"status":460`)
}

func TestHandler_ClientFor_reuses_client_for_same_timeout_tuple(t *testing.T) {
	// Given
	group, target := testTarget(t, "http://127.0.0.1:8080", 100*time.Millisecond)
	handler := NewHandler()

	// When
	first := handler.ClientFor(target, group.MaxIdleConnsPerHost())
	second := handler.ClientFor(target, group.MaxIdleConnsPerHost())

	// Then
	require.Same(t, first, second)
}

func TestBufferRequestBody_opens_fresh_readers(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("replay me"))

	// When
	body, err := BufferRequestBody(request)
	first, firstErr := io.ReadAll(body.Open())
	second, secondErr := io.ReadAll(body.Open())

	// Then
	require.NoError(t, err)
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	require.Equal(t, []byte("replay me"), first)
	require.Equal(t, first, second)
}

func testTarget(t *testing.T, rawURL string, readTimeout time.Duration) (*targetgroup.TargetGroup, targetgroup.Target) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	registry, err := targetgroup.NewRegistry(map[config.TargetGroupID]config.TargetGroupConfig{
		"api": {
			MaxIdleConnsPerHost: 3,
			Targets: []config.TargetConfig{{
				Host: host, Port: port, Weight: 1, ReadTimeout: config.Milliseconds(readTimeout / time.Millisecond),
			}},
		},
	})
	require.NoError(t, err)
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	return group, group.Targets()[0]
}
