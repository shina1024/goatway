package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigFromEnv_prefersTraceSpecificSettings(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://traces.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://fallback.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")

	// When
	config, err := ConfigFromEnv()

	// Then
	require.NoError(t, err)
	require.Equal(t, "https://traces.example.test:4317", config.Endpoint)
	require.Equal(t, "grpc", config.Protocol)
}

func TestConfigFromEnv_usesGeneralEndpointWhenTraceSpecificEndpointIsEmpty(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	// When
	config, err := ConfigFromEnv()

	// Then
	require.NoError(t, err)
	require.Equal(t, "http://collector.example.test:4317", config.Endpoint)
	require.Equal(t, "grpc", config.Protocol)
}

func TestConfigFromEnv_prefersMetricsSpecificSettings(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://metrics.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://fallback.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://traces.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc")

	// When
	config, err := ConfigFromEnv()

	// Then
	require.NoError(t, err)
	require.Equal(t, "https://metrics.example.test:4317", config.MetricsEndpoint)
	require.Equal(t, "grpc", config.MetricsProtocol)
}

func TestConfigFromEnv_disablesExportWhenNoEndpointIsConfigured(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	// When
	config, err := ConfigFromEnv()

	// Then
	require.NoError(t, err)
	require.Empty(t, config.Endpoint)
	require.Equal(t, "grpc", config.Protocol)
	require.Empty(t, config.MetricsEndpoint)
	require.Equal(t, "grpc", config.MetricsProtocol)
}

func TestConfigFromEnv_rejectsInvalidConfiguredEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"collector.example.test:4317",
		"https:///v1/traces",
		"ftp://collector.example.test:4317",
	} {
		t.Run(endpoint, func(t *testing.T) {
			// Given
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoint)

			// When
			_, err := ConfigFromEnv()

			// Then
			require.Error(t, err)
		})
	}
}

func TestConfigFromEnv_rejectsNonGRPCProtocolWhenExportIsEnabled(t *testing.T) {
	// Given
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://collector.example.test:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")

	// When
	_, err := ConfigFromEnv()

	// Then
	require.Error(t, err)
}
