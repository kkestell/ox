package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func main() {
	logger := newLogger(os.Stderr, os.Getenv("OX_LOG_LEVEL"))
	logger.Info("ox starting")

	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		logger.Error("reading stdin", "error", err)
		os.Exit(1)
	}

	logger.Info("ox stopped")
}

func newLogger(output io.Writer, configuredLevel string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(configuredLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: level}))
}
