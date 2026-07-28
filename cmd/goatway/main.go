package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"goatway/internal/config"
	"goatway/internal/logging"
)

func main() {
	os.Exit(execute())
}

func loadOptionsFromEnv() config.LoadOptions {
	return config.LoadOptions{
		APITokensPath: os.Getenv("GOATWAY_API_CLIENT_TOKENS_FILE"),
		APITokensYAML: os.Getenv("GOATWAY_API_CLIENT_TOKENS_YAML"),
	}
}

func execute() int {
	configDir := flag.String("config", "./config", "configuration directory")
	listenAddr := flag.String("listen", ":8080", "listen address")
	flag.Parse()

	configFlagProvided := false
	flag.Visit(func(visited *flag.Flag) {
		if visited.Name == "config" {
			configFlagProvided = true
		}
	})
	resolvedConfigDir := resolveConfigDir(configDirInputs{
		envDir:         os.Getenv("GOATWAY_CONFIG_DIR"),
		flagDir:        *configDir,
		flagProvided:   configFlagProvided,
		executablePath: os.Executable,
		userConfigDir:  os.UserConfigDir,
		dirExists:      directoryExists,
	})
	if envAddr := os.Getenv("GOATWAY_LISTEN_ADDR"); envAddr != "" {
		*listenAddr = envAddr
	}

	logger := logging.New(os.Getenv("GOATWAY_ENV"), os.Stderr)
	logger.Info("configuration directory resolved", slog.String("config_dir", resolvedConfigDir))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	settings := runSettings{
		configDir:  resolvedConfigDir,
		listenAddr: *listenAddr,
		devMode:    os.Getenv("GOATWAY_ENV") == "dev",
		logger:     logger,
	}
	if err := run(ctx, settings, productionDependenciesWithOptions(loadOptionsFromEnv())); err != nil {
		logger.Error("goatway failed", slog.Any("err", err))
		return 1
	}
	return 0
}
