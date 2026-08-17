package main

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/openrouter"
)

var (
	name    = "ox"
	version = "0.0.1"
)

// Ox's handlers spend their time waiting on the provider and client, so this
// effectively removes jrpc2's handler semaphore as a limit. Concurrent turns
// are bounded by the sessions the client opened. If this bound were reached,
// the cancellation that could free a prompt handler would itself wait for a
// slot.
const handlerConcurrency = 1 << 30

func main() {
	logger := newLogger(os.Stderr, os.Getenv("OX_LOG_LEVEL"))
	environment := config.Environment{
		GlobalPath: config.GlobalPath(
			os.Getenv("XDG_CONFIG_HOME"),
			os.Getenv("HOME"),
		),
		ModelOverride: os.Getenv("OX_MODEL"),
	}
	logger.Info(
		"ox starting",
		"version", version,
		"global_config_path", environment.GlobalPath,
	)

	client := &openrouter.Client{
		APIKey:  os.Getenv("OPENROUTER_API_KEY"),
		BaseURL: os.Getenv("OX_OPENROUTER_BASE_URL"),
		Logger:  logger,
	}
	methods := agent.New(name, version, environment, client, logger).Methods()

	server := jrpc2.NewServer(methods, &jrpc2.ServerOptions{
		AllowPush:   true,
		Concurrency: handlerConcurrency,
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
