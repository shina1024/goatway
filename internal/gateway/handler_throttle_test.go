package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandler_rejects_over_limit_request_and_releases_slot_after_response(t *testing.T) {
	// Given
	started := make(chan struct{})
	release := make(chan struct{})
	var firstRequest sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		block := false
		firstRequest.Do(func() { block = true })
		if block {
			started <- struct{}{}
			<-release
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n")
	first := gatewayRequest()
	second := gatewayRequest()
	firstResult := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, first)
		firstResult <- recorder
	}()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("first request did not reach the upstream")
	}

	// When
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	close(release)
	firstRecorder := <-firstResult
	laterRecorder := httptest.NewRecorder()
	handler.ServeHTTP(laterRecorder, gatewayRequest())

	// Then
	require.Equal(t, http.StatusTooManyRequests, secondRecorder.Code)
	require.Equal(t, http.StatusNoContent, firstRecorder.Code)
	require.Equal(t, http.StatusNoContent, laterRecorder.Code)
}

func TestHandler_does_not_leak_throttle_slot_when_route_rejects_request(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*http.Request)
		want    int
	}{
		{"authentication fails", func(request *http.Request) { request.Header.Del(apiTokenHeader) }, http.StatusUnauthorized},
		{"IP authorization fails", func(request *http.Request) {
			request.Header.Set(apiTokenHeader, "public-token")
			request.RemoteAddr = "192.0.2.1:1234"
		}, http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			host, port := targetAddress(t, upstream.URL)
			handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n")
			rejected := gatewayRequest()
			test.prepare(rejected)

			// When
			rejectedRecorder := httptest.NewRecorder()
			handler.ServeHTTP(rejectedRecorder, rejected)
			acceptedRecorder := httptest.NewRecorder()
			handler.ServeHTTP(acceptedRecorder, gatewayRequest())

			// Then
			require.Equal(t, test.want, rejectedRecorder.Code)
			require.Equal(t, http.StatusNoContent, acceptedRecorder.Code)
		})
	}
}

func gatewayRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/products/42", nil)
	request.Header.Set(apiTokenHeader, "public-token")
	request.RemoteAddr = "127.0.0.1:1234"
	return request
}
