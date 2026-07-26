package health

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandler_returnsOK(t *testing.T) {
	// Given
	handler := Handler()

	// When
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	// Then
	require.Equal(t, http.StatusOK, response.Code)
}
