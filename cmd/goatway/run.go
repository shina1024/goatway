package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"goatway/internal/config"
	"goatway/internal/gateway"
	"goatway/internal/proxy"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/telemetry"
	"goatway/internal/throttle"
)

const (
	shutdownTimeout       = 10 * time.Second
	maxRequestHeaderBytes = 16 << 10
)

type telemetryRuntime interface {
	TracerProvider() trace.TracerProvider
	TraceContext() propagation.TraceContext
	HTTPHandler(http.Handler) http.Handler
	Shutdown(context.Context) error
}

type httpServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

type runSettings struct {
	configDir  string
	listenAddr string
	devMode    bool
	logger     *slog.Logger
}

type runDependencies struct {
	configFromEnv  func() (telemetry.Config, error)
	newRuntime     func(context.Context, telemetry.Config) (telemetryRuntime, error)
	loadConfig     func(string) (*config.Config, error)
	newFileFetcher func(string) throttle.Fetcher
	newServer      func(runSettings, http.Handler) httpServer
	poll           func(context.Context, *throttle.DeploymentTracker, throttle.Fetcher)
}

func productionDependencies() runDependencies {
	return runDependencies{
		configFromEnv: telemetry.ConfigFromEnv,
		newRuntime: func(ctx context.Context, telemetryConfig telemetry.Config) (telemetryRuntime, error) {
			return telemetry.New(ctx, telemetryConfig)
		},
		loadConfig:     config.Load,
		newFileFetcher: func(path string) throttle.Fetcher { return throttle.NewFileFetcher(path) },
		newServer: func(settings runSettings, handler http.Handler) httpServer {
			return &http.Server{
				Addr:              settings.listenAddr,
				Handler:           handler,
				ReadTimeout:       5 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				WriteTimeout:      10 * time.Second,
				IdleTimeout:       120 * time.Second,
				MaxHeaderBytes:    maxRequestHeaderBytes,
			}
		},
		poll: func(ctx context.Context, tracker *throttle.DeploymentTracker, fetcher throttle.Fetcher) {
			tracker.Poll(ctx, fetcher, time.Second)
		},
	}
}

func run(ctx context.Context, settings runSettings, dependencies runDependencies) (result error) {
	telemetryConfig, err := dependencies.configFromEnv()
	if err != nil {
		return fmt.Errorf("read telemetry configuration: %w", err)
	}
	runtime, err := dependencies.newRuntime(ctx, telemetryConfig)
	if err != nil {
		return fmt.Errorf("create telemetry runtime: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := runtime.Shutdown(shutdownCtx); err != nil {
			result = errors.Join(result, fmt.Errorf("shutdown telemetry runtime: %w", err))
		}
	}()

	configuration, err := dependencies.loadConfig(settings.configDir)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	registry, err := targetgroup.NewRegistry(configuration.TargetGroups)
	if err != nil {
		return fmt.Errorf("build target group registry: %w", err)
	}
	routes, err := router.New(*configuration)
	if err != nil {
		return fmt.Errorf("build router: %w", err)
	}
	limits := make(map[string]int, len(configuration.MaxConcurrentRequests))
	for client, maximum := range configuration.MaxConcurrentRequests {
		limits[string(client)] = maximum
	}
	limiter := throttle.NewLimiterFromLimits(limits)
	tracker := throttle.NewDeploymentTracker(throttle.WithLogger(settings.logger))
	if err := tracker.SetDepType(); err != nil {
		return fmt.Errorf("detect deployment type: %w", err)
	}
	fetcher := dependencies.newFileFetcher(filepath.Join(settings.configDir, "deployment.yml"))
	forwarder := proxy.NewHandler(
		proxy.WithLogger(settings.logger),
		proxy.WithTelemetry(runtime.TracerProvider(), runtime.TraceContext()),
	)
	handler := gateway.NewHandler(
		configuration,
		registry,
		routes,
		limiter,
		tracker,
		gateway.WithProxy(forwarder),
		gateway.WithLogger(settings.logger),
		gateway.WithDevMode(settings.devMode),
	)
	server := dependencies.newServer(settings, runtime.HTTPHandler(handler))

	pollCtx, cancelPoll := context.WithCancel(context.WithoutCancel(ctx))
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		dependencies.poll(pollCtx, tracker, fetcher)
	}()
	defer func() {
		cancelPoll()
		<-pollDone
	}()

	return serve(ctx, server)
}

func serve(ctx context.Context, server httpServer) error {
	listenResult := make(chan error, 1)
	go func() { listenResult <- server.ListenAndServe() }()

	var listenErr error
	listenReceived := false
	select {
	case listenErr = <-listenResult:
		listenReceived = true
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancel()
	if !listenReceived {
		listenErr = <-listenResult
	}
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}

	var listenFailure error
	if listenErr != nil {
		listenFailure = fmt.Errorf("listen and serve: %w", listenErr)
	}
	var shutdownFailure error
	if shutdownErr != nil {
		shutdownFailure = fmt.Errorf("shutdown HTTP server: %w", shutdownErr)
	}
	return errors.Join(listenFailure, shutdownFailure)
}
