// Package httperr writes structured JSON error responses for the gateway.
package httperr

import (
	"context"
	"encoding/json"
	"net/http"

	"goatway/internal/telemetry"
)

// Envelope is the structured error body returned to API clients.
type Envelope struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

// Code returns the stable error code for an HTTP status.
func Code(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	case http.StatusBadGateway:
		return "bad_gateway"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "gateway_timeout"
	default:
		return "internal_error"
	}
}

// Write emits a JSON error envelope for status, including the trace ID from ctx.
func Write(writer http.ResponseWriter, ctx context.Context, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(Envelope{
		Status:  status,
		Code:    code,
		Message: http.StatusText(status),
		TraceID: telemetry.TraceID(ctx),
	})
}
