package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"goatway/internal/throttle"
)

func Test_run_shuts_down_HTTP_then_poller_then_telemetry_when_context_canceled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenStarted := make(chan struct{})
	pollStarted := make(chan struct{})
	listenReleased := make(chan struct{})
	httpShutdownReturned := make(chan struct{})
	pollCanceled := make(chan struct{})
	pollJoined := make(chan struct{})
	contextErrors := make(chan error, 2)
	orderingErrors := make(chan error, 1)
	configDir := testConfigDir(t, "http://127.0.0.1:8080")

	server := testServer{
		listen: func() error {
			close(listenStarted)
			<-listenReleased
			return http.ErrServerClosed
		},
		shutdown: func(shutdownCtx context.Context) error {
			contextErrors <- shutdownContextError(shutdownCtx)
			select {
			case <-pollCanceled:
				return errors.New("poller canceled before HTTP shutdown returned")
			default:
			}
			defer close(httpShutdownReturned)
			close(listenReleased)
			return nil
		},
	}
	runtime := &testRuntime{shutdown: func(shutdownCtx context.Context) error {
		contextErrors <- shutdownContextError(shutdownCtx)
		select {
		case <-pollJoined:
			return nil
		default:
			return errors.New("telemetry shutdown started before poller joined")
		}
	}}
	dependencies := testDependencies(runtime, server)
	dependencies.poll = func(pollCtx context.Context, _ *throttle.DeploymentTracker, _ throttle.Fetcher) {
		close(pollStarted)
		<-pollCtx.Done()
		close(pollCanceled)
		select {
		case <-httpShutdownReturned:
		default:
			orderingErrors <- errors.New("poller canceled before HTTP shutdown returned")
		}
		close(pollJoined)
	}

	// When
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, testSettings(configDir), dependencies) }()
	<-listenStarted
	<-pollStarted
	cancel()
	err := <-errCh

	// Then
	require.NoError(t, err)
	require.NoError(t, <-contextErrors)
	require.NoError(t, <-contextErrors)
	select {
	case orderingErr := <-orderingErrors:
		require.NoError(t, orderingErr)
	default:
	}
}

func Test_run_retains_HTTP_and_telemetry_errors_when_shutdown_times_out(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenStarted := make(chan struct{})
	shutdownCalled := make(chan struct{})
	telemetryFailure := errors.New("telemetry shutdown failed")
	configDir := testConfigDir(t, "http://127.0.0.1:8080")
	server := testServer{
		listen: func() error {
			close(listenStarted)
			<-shutdownCalled
			return http.ErrServerClosed
		},
		shutdown: func(context.Context) error {
			close(shutdownCalled)
			return context.DeadlineExceeded
		},
	}
	runtime := &testRuntime{shutdown: func(context.Context) error { return telemetryFailure }}

	// When
	dependencies := testDependencies(runtime, server)
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, testSettings(configDir), dependencies) }()
	<-listenStarted
	cancel()
	err := <-errCh

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, telemetryFailure)
}

func Test_run_retains_listen_error_and_runs_cleanup(t *testing.T) {
	// Given
	listenFailure := errors.New("listen failed")
	shutdownCalled := make(chan struct{})
	telemetryShutdown := make(chan struct{})
	server := testServer{
		listen: func() error { return listenFailure },
		shutdown: func(context.Context) error {
			close(shutdownCalled)
			return nil
		},
	}
	runtime := &testRuntime{shutdown: func(context.Context) error {
		close(telemetryShutdown)
		return nil
	}}

	// When
	err := run(context.Background(), testSettings(testConfigDir(t, "http://127.0.0.1:8080")), testDependencies(runtime, server))

	// Then
	require.ErrorIs(t, err, listenFailure)
	<-shutdownCalled
	<-telemetryShutdown
}

func shutdownContextError(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("shutdown context was already canceled")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("shutdown context had no deadline")
	}
	return nil
}
