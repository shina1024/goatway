package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"goatway/internal/logging"
)

func main() {
	os.Exit(execute())
}

func execute() int {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	settings := runSettings{
		configDir:  *configDir,
		listenAddr: *listenAddr,
		devMode:    os.Getenv("GOATWAY_ENV") == "dev",
		logger:     logger,
	}
	if err := run(ctx, settings, productionDependencies()); err != nil {
		logger.Error("goatway failed", slog.Any("err", err))
		return 1
	}
	return 0
}
