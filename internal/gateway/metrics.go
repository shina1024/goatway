package gateway

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *metricsResponseWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *metricsResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (handler *Handler) recordRequestMetrics(ctx context.Context, started time.Time, method string, status int, client string, targetGroup string) {
	if handler.metrics == nil || status == 0 {
		return
	}
	statusClass := fmt.Sprintf("%dxx", status/100)
	requestAttributes := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("status_class", statusClass),
		attribute.String("client", client),
		attribute.String("target_group", targetGroup),
	)
	handler.metrics.Requests.Add(ctx, 1, requestAttributes)
	handler.metrics.RequestDuration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("status_class", statusClass),
		attribute.String("target_group", targetGroup),
	))
}

func (handler *Handler) recordThrottleRejection(ctx context.Context, client string) {
	if handler.metrics == nil {
		return
	}
	handler.metrics.ThrottleRejections.Add(ctx, 1, metric.WithAttributes(attribute.String("client", client)))
}
