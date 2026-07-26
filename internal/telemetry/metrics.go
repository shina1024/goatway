package telemetry

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// Metrics contains goatway's low-cardinality RED metric instruments.
type Metrics struct {
	Requests           metric.Int64Counter
	RequestDuration    metric.Float64Histogram
	Errors             metric.Int64Counter
	Retries            metric.Int64Counter
	ThrottleRejections metric.Int64Counter
}

// NewMetrics creates the instruments used by gateway and proxy handlers.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	requests, err := meter.Int64Counter("goatway.requests")
	if err != nil {
		return nil, fmt.Errorf("create request counter: %w", err)
	}
	requestDuration, err := meter.Float64Histogram("goatway.request.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create request duration histogram: %w", err)
	}
	errors, err := meter.Int64Counter("goatway.errors")
	if err != nil {
		return nil, fmt.Errorf("create error counter: %w", err)
	}
	retries, err := meter.Int64Counter("goatway.retries")
	if err != nil {
		return nil, fmt.Errorf("create retry counter: %w", err)
	}
	throttleRejections, err := meter.Int64Counter("goatway.throttle.rejections")
	if err != nil {
		return nil, fmt.Errorf("create throttle rejection counter: %w", err)
	}
	return &Metrics{
		Requests:           requests,
		RequestDuration:    requestDuration,
		Errors:             errors,
		Retries:            retries,
		ThrottleRejections: throttleRejections,
	}, nil
}
