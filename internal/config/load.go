package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(dir string) (*Config, error) {
	return LoadWithOptions(dir, LoadOptions{})
}

func LoadWithOptions(dir string, opts LoadOptions) (*Config, error) {
	config := &Config{}
	if err := decodeFile(filepath.Join(dir, "target_groups.yml"), &config.TargetGroups); err != nil {
		return nil, fmt.Errorf("load target groups: %w", err)
	}
	if err := decodeFile(filepath.Join(dir, "routes.yml"), &config.Routes); err != nil {
		return nil, fmt.Errorf("load routes: %w", err)
	}
	if err := decodeTokens(dir, opts, &config.APIClientTokens); err != nil {
		return nil, fmt.Errorf("load API client tokens: %w", err)
	}
	if err := decodeFile(filepath.Join(dir, "ip_range_groups.yml"), &config.IPRangeGroups); err != nil {
		return nil, fmt.Errorf("load IP range groups: %w", err)
	}
	if err := decodeFile(filepath.Join(dir, "max_concurrent_requests.yml"), &config.MaxConcurrentRequests); err != nil {
		return nil, fmt.Errorf("load maximum concurrent requests: %w", err)
	}
	if err := decodeFile(filepath.Join(dir, "deployment.yml"), &config.Deployment); err != nil {
		return nil, fmt.Errorf("load deployment: %w", err)
	}
	gatewayPath := filepath.Join(dir, "gateway.yml")
	gatewayFilePresent := true
	if err := decodeFile(gatewayPath, &config.Gateway); err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			gatewayFilePresent = false
		} else if !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("load gateway: %w", err)
		}
	}
	config.gatewayFilePresent = gatewayFilePresent
	config.Gateway = config.Gateway.withDefaults()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate configuration: %w", err)
	}
	return config, nil
}

func decodeTokens[T any](dir string, opts LoadOptions, destination *T) error {
	if opts.APITokensYAML != "" {
		return decodeReader("GOATWAY_API_CLIENT_TOKENS_YAML", strings.NewReader(opts.APITokensYAML), destination)
	}
	if opts.APITokensPath != "" {
		return decodeFile(opts.APITokensPath, destination)
	}
	return decodeFile(filepath.Join(dir, "api_client_tokens.yml"), destination)
}

func decodeFile[T any](path string, destination *T) (err error) {
	r, err := os.Open(path) //nolint:gosec // paths are constructed from the trusted configuration directory
	if err != nil {
		return &DecodeError{File: path, Err: err}
	}
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			err = &DecodeError{File: path, Err: closeErr}
		}
	}()

	return decodeReader(path, r, destination)
}

func decodeReader[T any](source string, r io.Reader, destination *T) error {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return &DecodeError{File: source, Err: err}
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return &DecodeError{File: source, Err: errors.New("multiple YAML documents")}
		}
		return &DecodeError{File: source, Err: err}
	}
	return nil
}
