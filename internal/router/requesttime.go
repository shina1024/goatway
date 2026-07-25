package router

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"goatway/internal/headers"
)

type requestTimeContextKey struct{}

// WithRequestTimeOverride records a development-only request timestamp in context.
// When devMode is false the header is ignored without reading the environment.
func WithRequestTimeOverride(request *http.Request, devMode bool) (*http.Request, error) {
	if !devMode {
		return request, nil
	}

	values := request.Header.Values(headers.RequestTime)
	if len(values) == 0 {
		return request, nil
	}
	requestTime, err := time.Parse(time.RFC3339, values[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequestTime, err)
	}
	contextWithTime := context.WithValue(request.Context(), requestTimeContextKey{}, requestTime)
	return request.WithContext(contextWithTime), nil
}

// RequestTime retrieves a request timestamp installed by WithRequestTimeOverride.
func RequestTime(ctx context.Context) (time.Time, bool) {
	requestTime, exists := ctx.Value(requestTimeContextKey{}).(time.Time)
	return requestTime, exists
}
