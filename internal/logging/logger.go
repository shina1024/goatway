package logging

import (
	"io"
	"log/slog"
)

// New creates a development text logger or production JSON logger.
func New(environment string, output io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if environment == "dev" {
		return slog.New(slog.NewTextHandler(output, options))
	}
	return slog.New(slog.NewJSONHandler(output, options))
}
