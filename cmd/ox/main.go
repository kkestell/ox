package main

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/kkestell/ox/internal/agent"
)

var (
	name    = "ox"
	version = "0.0.1"
)

func main() {
	logger := newLogger(os.Stderr, os.Getenv("OX_LOG_LEVEL"))
	logger.Info("ox starting", "version", version)

	server := jrpc2.NewServer(agent.New(name, version, logger).Methods(), &jrpc2.ServerOptions{
		AllowPush: true,
	})
	server.Start(channel.Line(os.Stdin, os.Stdout))
	if err := server.Wait(); err != nil {
		logger.Error("ox stopped with error", "error", err)
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
