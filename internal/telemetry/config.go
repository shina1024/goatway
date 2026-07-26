package telemetry

import (
	"errors"
	"fmt"
	"net/url"
	"os"
)

const grpcProtocol = "grpc"

var (
	errInvalidEndpoint            = errors.New("telemetry: invalid OTLP traces endpoint")
	errInvalidMetricsEndpoint     = errors.New("telemetry: invalid OTLP metrics endpoint")
	errUnsupportedProtocol        = errors.New("telemetry: unsupported OTLP traces protocol")
	errUnsupportedMetricsProtocol = errors.New("telemetry: unsupported OTLP metrics protocol")
)

// Config controls optional OTLP trace and metric export.
type Config struct {
	Endpoint        string
	Protocol        string
	MetricsEndpoint string
	MetricsProtocol string
}

// ConfigFromEnv reads the standard OTLP trace and metric environment variables.
func ConfigFromEnv() (Config, error) {
	endpoint := firstSet("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
	protocol := firstSet("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	metricsEndpoint := firstSet("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
	metricsProtocol := firstSet("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	config := (Config{Endpoint: endpoint, Protocol: protocol, MetricsEndpoint: metricsEndpoint, MetricsProtocol: metricsProtocol}).normalized()
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) normalized() Config {
	if config.Protocol == "" {
		config.Protocol = grpcProtocol
	}
	if config.MetricsProtocol == "" {
		config.MetricsProtocol = grpcProtocol
	}
	return config
}

func (config Config) validate() error {
	if config.Endpoint == "" {
		return config.validateMetrics()
	}
	if err := validateEndpoint(config.Endpoint); err != nil {
		return err
	}
	if config.Protocol != grpcProtocol {
		return fmt.Errorf("%w: %q", errUnsupportedProtocol, config.Protocol)
	}
	return config.validateMetrics()
}

func (config Config) validateMetrics() error {
	if config.MetricsEndpoint == "" {
		return nil
	}
	if err := validateMetricsEndpoint(config.MetricsEndpoint); err != nil {
		return err
	}
	if config.MetricsProtocol != grpcProtocol {
		return fmt.Errorf("%w: %q", errUnsupportedMetricsProtocol, config.MetricsProtocol)
	}
	return nil
}

func firstSet(primary string, fallback string) string {
	if value := os.Getenv(primary); value != "" {
		return value
	}
	return os.Getenv(fallback)
}

func validateEndpoint(endpoint string) error {
	return validateEndpointWithError(endpoint, errInvalidEndpoint)
}

func validateMetricsEndpoint(endpoint string) error {
	return validateEndpointWithError(endpoint, errInvalidMetricsEndpoint)
}

func validateEndpointWithError(endpoint string, invalidEndpoint error) error {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return fmt.Errorf("%w %q: %w", invalidEndpoint, endpoint, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("%w %q: require an http or https URL with a host", invalidEndpoint, endpoint)
	}
	return nil
}
