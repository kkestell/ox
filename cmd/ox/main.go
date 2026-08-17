package main

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/openrouter"
)

var (
	name    = "ox"
	version = "0.0.1"
)

func main() {
	logger := newLogger(os.Stderr, os.Getenv("OX_LOG_LEVEL"))
	logger.Info("ox starting", "version", version)

	client := &openrouter.Client{
		APIKey:  os.Getenv("OPENROUTER_API_KEY"),
		BaseURL: os.Getenv("OX_OPENROUTER_BASE_URL"),
		Logger:  logger,
	}
	methods := agent.New(name, version, os.Getenv("OX_MODEL"), client, logger).Methods()

	server := jrpc2.NewServer(methods, &jrpc2.ServerOptions{
		AllowPush: true,
		// A prompt handler holds its slot for the whole turn, and jrpc2 bounds
		// handlers with a semaphore that defaults to the CPU count. On a
		// single-CPU machine session/cancel would then wait behind the very turn
		// it exists to stop. These handlers block on the network and on the
		// client rather than on the CPU, so the bound just has to stay well above
		// the number of live sessions.
		Concurrency: 16,
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
