package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"goatway/internal/telemetry"
)

func TestHandler_recordsRequestMetric_when_requestIsServedWithoutMetricsEndpoint(t *testing.T) {
	// Given
	reader, metrics := newManualMetrics(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false, WithMetrics(metrics))

	// When
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, gatewayRequest())

	// Then
	require.Equal(t, http.StatusNoContent, response.Code)
	var recorded metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &recorded))
	requests, ok := metricData(recorded, "goatway.requests").Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, requests.DataPoints, 1)
	require.EqualValues(t, 1, requests.DataPoints[0].Value)
	require.Equal(t, http.MethodGet, metricAttribute(t, requests.DataPoints[0].Attributes, "method"))
	require.Equal(t, "2xx", metricAttribute(t, requests.DataPoints[0].Attributes, "status_class"))
	require.Equal(t, "public", metricAttribute(t, requests.DataPoints[0].Attributes, "client"))
	require.Equal(t, "catalog", metricAttribute(t, requests.DataPoints[0].Attributes, "target_group"))
	duration, ok := metricData(recorded, "goatway.request.duration").Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, duration.DataPoints, 1)
	require.EqualValues(t, 1, duration.DataPoints[0].Count)
	require.Positive(t, duration.DataPoints[0].Sum)
}

func TestHandler_recordsThrottleRejectionMetric_when_requestIsOverLimit(t *testing.T) {
	// Given
	reader, metrics := newManualMetrics(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	host, port := targetAddress(t, upstream.URL)
	handler := newTestHandler(t, testConfig(t, host, port), "public: 1\n", false, WithMetrics(metrics))
	firstResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, gatewayRequest())
		firstResult <- response
	}()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("first request did not reach the upstream")
	}

	// When
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, gatewayRequest())
	close(release)
	<-firstResult

	// Then
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	var recorded metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &recorded))
	rejections, ok := metricData(recorded, "goatway.throttle.rejections").Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, rejections.DataPoints, 1)
	require.EqualValues(t, 1, rejections.DataPoints[0].Value)
	require.Equal(t, "public", metricAttribute(t, rejections.DataPoints[0].Attributes, "client"))
}

func newManualMetrics(t *testing.T) (*sdkmetric.ManualReader, *telemetry.Metrics) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewMetrics(provider.Meter("goatway"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	return reader, metrics
}

func metricData(recorded metricdata.ResourceMetrics, name string) metricdata.Metrics {
	for _, scope := range recorded.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				return metric
			}
		}
	}
	panic("metric " + name + " was not recorded")
}

func metricAttribute(t *testing.T, attributes attribute.Set, key string) string {
	t.Helper()
	value, ok := attributes.Value(attribute.Key(key))
	require.Truef(t, ok, "metric attribute %q was not recorded", key)
	return value.AsString()
}
