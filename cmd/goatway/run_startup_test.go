package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"goatway/internal/config"
	"goatway/internal/telemetry"
)

func Test_run_joins_startup_and_telemetry_shutdown_errors_after_runtime_creation(t *testing.T) {
	// Given
	startupFailure := errors.New("configuration failed")
	shutdownFailure := errors.New("telemetry shutdown failed")
	shutdowns := 0
	runtime := &testRuntime{shutdown: func(context.Context) error {
		shutdowns++
		return shutdownFailure
	}}
	dependencies := testDependencies(runtime, testServer{
		listen:   func() error { return nil },
		shutdown: func(context.Context) error { return nil },
	})
	dependencies.loadConfig = func(string) (*config.Config, error) {
		return nil, startupFailure
	}

	// When
	err := run(context.Background(), testSettings(t.TempDir()), dependencies)

	// Then
	require.ErrorIs(t, err, startupFailure)
	require.ErrorIs(t, err, shutdownFailure)
	require.Equal(t, 1, shutdowns)
}

func Test_run_does_not_create_runtime_when_telemetry_environment_is_invalid(t *testing.T) {
	// Given
	configurationFailure := errors.New("telemetry configuration failed")
	runtimeCreations := 0
	dependencies := productionDependencies()
	dependencies.configFromEnv = func() (telemetry.Config, error) {
		return telemetry.Config{}, configurationFailure
	}
	dependencies.newRuntime = func(context.Context, telemetry.Config) (telemetryRuntime, error) {
		runtimeCreations++
		return &testRuntime{}, nil
	}

	// When
	err := run(context.Background(), testSettings(t.TempDir()), dependencies)

	// Then
	require.ErrorIs(t, err, configurationFailure)
	require.Zero(t, runtimeCreations)
}
