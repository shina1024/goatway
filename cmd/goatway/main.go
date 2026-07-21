package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"goatway/internal/config"
	"goatway/internal/gateway"
	"goatway/internal/logging"
	"goatway/internal/router"
	"goatway/internal/targetgroup"
	"goatway/internal/throttle"
)

func main() {
	configDir := flag.String("config", "./config", "configuration directory")
	listenAddr := flag.String("listen", ":8080", "listen address")
	flag.Parse()

	if envDir := os.Getenv("GOATWAY_CONFIG_DIR"); envDir != "" {
		*configDir = envDir
	}
	if envAddr := os.Getenv("GOATWAY_LISTEN_ADDR"); envAddr != "" {
		*listenAddr = envAddr
	}

	logger := logging.New(os.Getenv("GOATWAY_ENV"), os.Stderr)

	cfg, err := config.Load(*configDir)
	if err != nil {
		logger.Error("failed to load configuration", slog.Any("err", err))
		os.Exit(1)
	}

	registry, err := targetgroup.NewRegistry(cfg.TargetGroups)
	if err != nil {
		logger.Error("failed to build target group registry", slog.Any("err", err))
		os.Exit(1)
	}

	routes, err := router.New(*cfg)
	if err != nil {
		logger.Error("failed to build router", slog.Any("err", err))
		os.Exit(1)
	}

	limiter, err := throttle.NewLimiter(filepath.Join(*configDir, "max_concurrent_requests.yml"))
	if err != nil {
		logger.Error("failed to build throttle limiter", slog.Any("err", err))
		os.Exit(1)
	}

	tracker := throttle.NewDeploymentTracker()
	if err := tracker.SetDepType(); err != nil {
		logger.Error("failed to detect deployment type", slog.Any("err", err))
		os.Exit(1)
	}

	fetcher := throttle.NewFileFetcher(filepath.Join(*configDir, "deployment.yml"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tracker.Poll(ctx, fetcher, time.Second)

	handler := gateway.NewHandler(cfg, registry, routes, limiter, tracker, gateway.WithLogger(logger))

	server := &http.Server{
		Addr:         *listenAddr,
		Handler:      handler,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		logger.Info("shutting down gracefully")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", slog.Any("err", err))
		}
		cancel()
		signal.Stop(sig)
	}()

	logger.Info("goatway started", slog.String("addr", *listenAddr), slog.String("config_dir", *configDir))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", slog.Any("err", err))
		os.Exit(1)
	}
}
