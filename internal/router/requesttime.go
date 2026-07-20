package router

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

const requestTimeHeader = "X-Goatway-Request-Time"

type requestTimeContextKey struct{}

// WithRequestTimeOverride records a development-only request timestamp in context.
func WithRequestTimeOverride(request *http.Request) (*http.Request, error) {
	if os.Getenv("GOATWAY_ENV") != "dev" {
		return request, nil
	}

	values := request.Header.Values(requestTimeHeader)
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
