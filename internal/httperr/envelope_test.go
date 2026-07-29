package httperr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrite_emitsJSONEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	Write(recorder, context.Background(), http.StatusNotFound, Code(http.StatusNotFound))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var envelope Envelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(t, http.StatusNotFound, envelope.Status)
	require.Equal(t, "not_found", envelope.Code)
	require.Equal(t, "Not Found", envelope.Message)
}

func TestCode_mapsStatuses(t *testing.T) {
	require.Equal(t, "bad_request", Code(http.StatusBadRequest))
	require.Equal(t, "unauthorized", Code(http.StatusUnauthorized))
	require.Equal(t, "forbidden", Code(http.StatusForbidden))
	require.Equal(t, "not_found", Code(http.StatusNotFound))
	require.Equal(t, "too_many_requests", Code(http.StatusTooManyRequests))
	require.Equal(t, "internal_error", Code(http.StatusInternalServerError))
	require.Equal(t, "bad_gateway", Code(http.StatusBadGateway))
	require.Equal(t, "service_unavailable", Code(http.StatusServiceUnavailable))
	require.Equal(t, "gateway_timeout", Code(http.StatusGatewayTimeout))
	require.Equal(t, "internal_error", Code(http.StatusTeapot))
}
