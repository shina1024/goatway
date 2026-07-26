package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"goatway/internal/config"
	"goatway/internal/telemetry"
)

func TestHandler_recordsRetryMetric_when_serverErrorIsRetried(t *testing.T) {
	// Given
	reader, metrics := newManualMetrics(t)
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{"api": {
		Targets:     []config.TargetConfig{retryTarget(t, backend.URL, time.Second), retryTarget(t, backend.URL, time.Second)},
		MaxTryCount: 2,
		RetryCases:  []string{"server_error"},
	}})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	handler := NewHandler(WithMetrics(metrics), WithRetrySleeper(func(time.Duration) {}))

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	var recorded metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &recorded))
	retries, ok := metricData(recorded, "goatway.retries").Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, retries.DataPoints, 1)
	require.EqualValues(t, 1, retries.DataPoints[0].Value)
	require.Equal(t, "api", metricAttribute(t, retries.DataPoints[0].Attributes, "target_group"))
}

func TestHandler_recordsErrorMetric_when_serverErrorIsFinal(t *testing.T) {
	// Given
	reader, metrics := newManualMetrics(t)
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	registry := newRetryRegistry(t, map[config.TargetGroupID]config.TargetGroupConfig{"api": {
		Targets:     []config.TargetConfig{retryTarget(t, backend.URL, time.Second)},
		MaxTryCount: 1,
		RetryCases:  []string{"server_error"},
	}})
	group, err := registry.Lookup("api")
	require.NoError(t, err)
	handler := NewHandler(WithMetrics(metrics))

	// When
	result, err := handler.ForwardWithRetry(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), retryInput(group, map[string]string{"api": "/"}))

	// Then
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, result.StatusCode)
	var recorded metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &recorded))
	errors, ok := metricData(recorded, "goatway.errors").Data.(metricdata.Sum[int64])
	require.True(t, ok)
	require.Len(t, errors.DataPoints, 1)
	require.EqualValues(t, 1, errors.DataPoints[0].Value)
	require.Equal(t, "server_error", metricAttribute(t, errors.DataPoints[0].Attributes, "error_type"))
	require.Equal(t, "api", metricAttribute(t, errors.DataPoints[0].Attributes, "target_group"))
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
