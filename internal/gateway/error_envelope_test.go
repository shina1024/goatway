package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"goatway/internal/header"
	"goatway/internal/httperr"
)

func TestHandler_returnsJSONErrorEnvelope_for_gatewayErrors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false)

	tests := []struct {
		name   string
		token  string
		path   string
		status int
		code   string
	}{
		{name: "missing token", token: "", path: "/products/42", status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "unknown token", token: "wrong", path: "/products/42", status: http.StatusForbidden, code: "forbidden"},
		{name: "no route", token: "public-token", path: "/nomatch", status: http.StatusNotFound, code: "not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.RemoteAddr = "127.0.0.1:1234"
			if test.token != "" {
				request.Header.Set(header.APIToken, test.token)
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			require.Equal(t, test.status, response.Code)
			require.Equal(t, "application/json", response.Header().Get("Content-Type"))
			var envelope httperr.Envelope
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Equal(t, test.status, envelope.Status)
			require.Equal(t, test.code, envelope.Code)
			require.NotEmpty(t, envelope.TraceID)
		})
	}
}
