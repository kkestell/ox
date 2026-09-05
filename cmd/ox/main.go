package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
	"github.com/kkestell/ox/internal/tools"
	diagnostictrace "github.com/kkestell/ox/internal/trace"
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
	switch arguments := os.Args[1:]; {
	case len(arguments) == 0:
		if err := serve(os.Stdin, os.Stdout, logger, diagnostictrace.Trace{}); err != nil {
			logger.Error("ox stopped with error", "error", err)
			os.Exit(1)
		}
	case len(arguments) == 2 && arguments[0] == "--trace" && arguments[1] != "":
		tracer, err := diagnostictrace.Open(arguments[1], func(err error) {
			logger.Error("diagnostic trace disabled after write failure", "error", err)
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ox: create diagnostic trace: %v\n", err)
			os.Exit(1)
		}
		serveErr := serve(os.Stdin, os.Stdout, logger, tracer)
		closeErr := tracer.Close()
		if serveErr != nil {
			logger.Error("ox stopped with error", "error", serveErr)
			os.Exit(1)
		}
		if closeErr != nil {
			logger.Error("close diagnostic trace", "error", closeErr)
			os.Exit(1)
		}
	case len(arguments) == 1 && arguments[0] == "login":
		credentialStore := credentials.NewStore(logger)
		client := &openrouter.Client{
			BaseURL: os.Getenv("OX_OPENROUTER_BASE_URL"),
			Logger:  logger,
		}
		if err := login(
			context.Background(),
			os.Stdin,
			os.Stderr,
			credentialStore,
			client,
		); err != nil {
			fmt.Fprintf(os.Stderr, "ox login: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: ox [--trace path] | ox login")
		os.Exit(2)
	}
}

func serve(
	input io.ReadCloser,
	output io.WriteCloser,
	logger *slog.Logger,
	tracer diagnostictrace.Trace,
) error {
	settingsPath := settings.GlobalPath(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("HOME"))
	sessionPath := agent.SessionPath(os.Getenv("XDG_DATA_HOME"), os.Getenv("HOME"))
	credentialStore := credentials.NewStore(logger)
	logger.Info(
		"ox starting",
		"version", version,
		"global_settings_path", settingsPath,
		"session_store_path", sessionPath,
		"credential_source", credentialStore.Source(),
	)

	client := &openrouter.Client{
		APIKey:  credentialStore.Key,
		BaseURL: os.Getenv("OX_OPENROUTER_BASE_URL"),
		Logger:  logger,
	}
	instance, err := agent.New(agent.Config{
		Name:          name,
		Version:       version,
		Logger:        logger,
		Credentials:   credentialStore,
		ModelOverride: os.Getenv("OX_MODEL"),
		SettingsPath:  settingsPath,
		SessionDir:    sessionPath,
		Client:        client,
		Tools:         tools.All(),
		Trace:         tracer,
	})
	if err != nil {
		return fmt.Errorf("configure ox: %w", err)
	}

	server := jrpc2.NewServer(instance.Methods(), &jrpc2.ServerOptions{
		AllowPush:   true,
		Concurrency: handlerConcurrency,
	})
	server.Start(channel.Line(input, output))
	if err := server.Wait(); err != nil {
		return err
	}

	logger.Info("ox stopped")
	return nil
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
