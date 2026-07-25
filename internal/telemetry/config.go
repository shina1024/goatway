package telemetry

import (
	"errors"
	"fmt"
	"net/url"
	"os"
)

const grpcProtocol = "grpc"

var (
	errInvalidEndpoint     = errors.New("telemetry: invalid OTLP traces endpoint")
	errUnsupportedProtocol = errors.New("telemetry: unsupported OTLP traces protocol")
)

// Config controls optional OTLP trace export.
type Config struct {
	Endpoint string
	Protocol string
}

// ConfigFromEnv reads the standard OTLP trace environment variables.
func ConfigFromEnv() (Config, error) {
	endpoint := firstSet("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
	protocol := firstSet("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")
	config := (Config{Endpoint: endpoint, Protocol: protocol}).normalized()
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) normalized() Config {
	if config.Protocol == "" {
		config.Protocol = grpcProtocol
	}
	return config
}

func (config Config) validate() error {
	if config.Endpoint == "" {
		return nil
	}
	if err := validateEndpoint(config.Endpoint); err != nil {
		return err
	}
	if config.Protocol != grpcProtocol {
		return fmt.Errorf("%w: %q", errUnsupportedProtocol, config.Protocol)
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
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return fmt.Errorf("%w %q: %w", errInvalidEndpoint, endpoint, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("%w %q: require an http or https URL with a host", errInvalidEndpoint, endpoint)
	}
	return nil
}
