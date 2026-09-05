package main

import (
	"context"
	"errors"
	"flag"
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

const (
	handlerConcurrency = 1 << 30
	usage              = "usage: ox [--log-level level] [--openrouter-base-url url] [--trace path] [--model id] [--credential-file path] [--no-keyring] [login]"
)

type optionalString struct {
	value string
	set   bool
}

func (v *optionalString) String() string { return v.value }
func (v *optionalString) Set(value string) error {
	v.value = value
	v.set = true
	return nil
}

type commandOptions struct {
	logLevel          optionalString
	openRouterBaseURL optionalString
	trace             optionalString
	model             optionalString
	credentialFile    optionalString
	noKeyring         bool
	command           string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(arguments []string, input *os.File, output io.WriteCloser, errorOutput io.Writer) int {
	options, err := parseArguments(arguments)
	if err != nil {
		fmt.Fprintf(errorOutput, "ox: %v\n%s\n", err, usage)
		return 2
	}
	settingsPath := settings.GlobalPath(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("HOME"))
	_, processConfig, err := settings.LoadGlobal(settingsPath)
	if err != nil {
		fmt.Fprintf(errorOutput, "ox: configure process: %v\n", err)
		return 1
	}
	process, err := settings.ResolveProcess(processConfig, settings.ProcessOverrides{
		LogLevel:          options.logLevel.pointer(),
		OpenRouterBaseURL: options.openRouterBaseURL.pointer(),
		Trace:             options.trace.pointer(),
	})
	if err != nil {
		fmt.Fprintf(errorOutput, "ox: configure process: %v\n", err)
		return 1
	}
	var fileCredential string
	if options.credentialFile.set {
		fileCredential, err = credentials.LoadFile(options.credentialFile.value)
		if err != nil {
			fmt.Fprintf(errorOutput, "ox: %v\n", err)
			return 1
		}
	}
	logger := newLogger(errorOutput, process.LogLevel)
	credentialStore := credentials.NewStore(logger, fileCredential, options.noKeyring)
	client := &openrouter.Client{BaseURL: process.OpenRouterBaseURL, Logger: logger}

	if options.command == "login" {
		if err := login(context.Background(), input, errorOutput, credentialStore, client); err != nil {
			fmt.Fprintf(errorOutput, "ox login: %v\n", err)
			return 1
		}
		return 0
	}

	tracer := diagnostictrace.Trace{}
	if process.Trace != "" {
		tracer, err = diagnostictrace.Open(process.Trace, func(err error) {
			logger.Error("diagnostic trace disabled after write failure", "error", err)
		})
		if err != nil {
			fmt.Fprintf(errorOutput, "ox: create diagnostic trace: %v\n", err)
			return 1
		}
	}
	serveErr := serve(input, output, logger, tracer, settingsPath, credentialStore, client, options.model.value)
	closeErr := tracer.Close()
	if serveErr != nil {
		logger.Error("ox stopped with error", "error", serveErr)
		return 1
	}
	if closeErr != nil {
		logger.Error("close diagnostic trace", "error", closeErr)
		return 1
	}
	return 0
}

func (v *optionalString) pointer() *string {
	if !v.set {
		return nil
	}
	return &v.value
}

func parseArguments(arguments []string) (commandOptions, error) {
	var options commandOptions
	flags := flag.NewFlagSet("ox", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Var(&options.logLevel, "log-level", "process log level")
	flags.Var(&options.openRouterBaseURL, "openrouter-base-url", "OpenRouter API base URL")
	flags.Var(&options.trace, "trace", "diagnostic trace path")
	flags.Var(&options.model, "model", "model override")
	flags.Var(&options.credentialFile, "credential-file", "OpenRouter credential file")
	flags.BoolVar(&options.noKeyring, "no-keyring", false, "disable OS keyring access")
	if err := flags.Parse(arguments); err != nil {
		return commandOptions{}, err
	}
	for name, value := range map[string]optionalString{
		"--log-level":           options.logLevel,
		"--openrouter-base-url": options.openRouterBaseURL,
		"--trace":               options.trace,
		"--model":               options.model,
		"--credential-file":     options.credentialFile,
	} {
		if value.set && strings.TrimSpace(value.value) == "" {
			return commandOptions{}, fmt.Errorf("%s must not be blank", name)
		}
	}
	rest := flags.Args()
	switch len(rest) {
	case 0:
	case 1:
		if rest[0] != "login" {
			return commandOptions{}, fmt.Errorf("unknown command %q", rest[0])
		}
		options.command = rest[0]
	default:
		return commandOptions{}, errors.New("too many arguments")
	}
	return options, nil
}

func serve(
	input io.ReadCloser,
	output io.WriteCloser,
	logger *slog.Logger,
	tracer diagnostictrace.Trace,
	settingsPath string,
	credentialStore *credentials.Store,
	client *openrouter.Client,
	modelOverride string,
) error {
	sessionPath := agent.SessionPath(os.Getenv("XDG_DATA_HOME"), os.Getenv("HOME"))
	memoryPath := agent.MemoryPath(os.Getenv("XDG_DATA_HOME"), os.Getenv("HOME"))
	logger.Info(
		"ox starting",
		"version", version,
		"global_settings_path", settingsPath,
		"session_store_path", sessionPath,
		"memory_store_path", memoryPath,
		"credential_source", credentialStore.Source(),
	)

	client.APIKey = credentialStore.Key
	instance, err := agent.New(agent.Config{
		Name:          name,
		Version:       version,
		Logger:        logger,
		Credentials:   credentialStore,
		ModelOverride: modelOverride,
		SettingsPath:  settingsPath,
		SessionDir:    sessionPath,
		MemoryDir:     memoryPath,
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
	waitErr := server.Wait()
	closeErr := instance.Close()
	if err := errors.Join(waitErr, closeErr); err != nil {
		return err
	}

	logger.Info("ox stopped")
	return nil
}

func newLogger(output io.Writer, configuredLevel string) *slog.Logger {
	level := slog.LevelInfo
	switch configuredLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: level}))
}
