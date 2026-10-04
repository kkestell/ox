// Command ox-server is the Ox server: an ACP agent over stdin and stdout, a
// headless prompt runner, and OpenRouter sign-in.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/control"
	"ox/internal/keyring"
	"ox/internal/openrouter"
	"ox/internal/server"
	"ox/internal/settings"
	"ox/internal/shellproc"
	"ox/internal/store"
	"ox/internal/sysprompt"
	"ox/internal/tools"
	"ox/internal/transcript"
)

const usage = `Usage:
  ox-server acp                     Serve the Ox server over stdin and stdout.
  ox-server run [--dir DIR] [--model MODEL] [--effort EFFORT] PROMPT
                                    Run one prompt and print the final answer.
  ox-server auth login openrouter   Verify an OpenRouter API key and save it.
  ox-server auth logout openrouter  Remove the saved OpenRouter API key.

ACP clients that append authentication commands can use
"acp auth login openrouter" and "acp auth logout openrouter".`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ox: %s\n", control.Escape(err.Error()))
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help" || len(args) == 2 && (args[1] == "-h" || args[1] == "--help")) {
		fmt.Println(usage)
		return nil
	}

	if len(args) >= 1 && args[0] == "acp" {
		if len(args) == 1 {
			return serveACP()
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return usageError("choose a command")
	}
	switch args[0] {
	case "auth":
		if len(args) != 3 || (args[1] != "login" && args[1] != "logout") {
			return usageError("expected auth login or auth logout with a provider")
		}
		if args[2] != catalog.Provider {
			return fmt.Errorf("%s is not a model provider; choose openrouter", args[2])
		}
		if args[1] == "login" {
			return login()
		}
		return logout()
	case "run":
		return runHeadless(args[1:])
	}
	return usageError("unknown command " + args[0])
}

func usageError(message string) error {
	return fmt.Errorf("%s\n\n%s", message, usage)
}

// apiKey returns `OPENROUTER_API_KEY`, which takes precedence, or the saved
// key.
func apiKey() (string, error) {
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		return key, nil
	}
	key, err := keyring.Get()
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", errors.New("model provider authentication required; run `ox auth login openrouter`")
	}
	return key, nil
}

// startup authenticates, fetches the model catalog, and reads the global
// settings against it.
func startup(ctx context.Context) (*agent.Agent, settings.Settings, string, error) {
	key, err := apiKey()
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	home, err := settings.Home()
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	client := openrouter.New(key)
	cat, err := client.FetchCatalog(ctx)
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	defaults, err := settings.Load(settings.GlobalPath(home), cat)
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	path, err := store.DatabasePath()
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	sessions, err := store.Open(path)
	if err != nil {
		return nil, settings.Settings{}, "", err
	}
	return agent.New(sessions, client, cat), defaults, home, nil
}

// signalContext returns a context cancelled by the termination signals that
// begin shutdown.
func signalContext() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}

// serveACP serves one ACP connection until stdin closes or a termination
// signal arrives. Either begins shutdown: new operations are rejected, active
// prompts are cancelled, and background processes are stopped, while active
// operations finish and respond.
func serveACP() error {
	ctx, stop := signalContext()
	defer stop()
	a, defaults, home, err := startup(ctx)
	if err != nil {
		return err
	}
	defer a.Store.Close()
	conn := acp.NewConn(os.Stdout)
	srv := server.New(conn, a, defaults, home)
	served := make(chan error, 1)
	go func() { served <- conn.Serve(os.Stdin, srv) }()
	select {
	case err = <-served:
	case <-ctx.Done():
	}
	srv.Shutdown()
	return err
}

type runOptions struct{ dir, model, effort, prompt string }

func parseRunOptions(args []string) (runOptions, error) {
	var options runOptions
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.dir, "dir", ".", "workspace directory")
	flags.StringVar(&options.model, "model", "", "model ID")
	flags.StringVar(&options.effort, "effort", "default", "reasoning effort")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 1 {
		return options, errors.New("expected one prompt after the options")
	}
	options.prompt = flags.Arg(0)
	if strings.TrimSpace(options.prompt) == "" {
		return options, errors.New("the prompt is blank")
	}
	return options, nil
}

// runHeadless runs one prompt in a new session in Auto mode and prints the
// answer. Skills are not invoked.
func runHeadless(args []string) error {
	options, err := parseRunOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(usage)
		return nil
	}
	if err != nil {
		return usageError(err.Error())
	}
	dir, model, effortID, prompt := options.dir, options.model, options.effort, options.prompt

	effort, ok := catalog.ParseEffort(effortID)
	if !ok {
		names := make([]string, len(catalog.Efforts))
		for i, effort := range catalog.Efforts {
			names[i] = string(effort)
		}
		return fmt.Errorf("%s is not an effort level; choose one of %s", effortID, strings.Join(names, ", "))
	}
	workspace, err := filepath.Abs(dir)
	if err == nil {
		workspace, err = filepath.EvalSymlinks(workspace)
	}
	if err != nil {
		return fmt.Errorf("opening %s: %w", dir, err)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", workspace)
	}

	ctx, stop := signalContext()
	defer stop()
	a, defaults, _, err := startup(ctx)
	if err != nil {
		return err
	}
	defer a.Store.Close()
	defaults, err = defaults.ForWorkspace(workspace, a.Catalog)
	if err != nil {
		return err
	}
	if model == "" {
		model = defaults.Model
	}
	selected := a.Catalog.Lookup(model)
	if selected == nil {
		return fmt.Errorf("%s is not a model; choose one of %s", model, strings.Join(a.Catalog.IDs(), ", "))
	}
	if !selected.Supports(effort) {
		var names []string
		for _, effort := range selected.Efforts {
			names = append(names, string(effort))
		}
		return fmt.Errorf("%s does not accept effort %s; choose one of %s", model, effort, strings.Join(names, ", "))
	}
	systemPrompt, err := sysprompt.ForWorkspace(workspace, tools.ShellProgram)
	if err != nil {
		return err
	}
	session, err := a.Store.Create(workspace)
	if err != nil {
		return err
	}
	processes := &shellproc.Processes{}
	defer processes.Shutdown()
	stopShutdown := context.AfterFunc(ctx, processes.BeginShutdown)
	defer stopShutdown()
	turn, err := a.Start(ctx, agent.Input{
		SessionID: session.ID, Input: transcript.TurnInput{Message: new(transcript.TextMessage(prompt))},
		Model: model, Effort: effort, Mode: transcript.ModeAuto, SystemPrompt: systemPrompt, Processes: processes,
	}, headless{})
	if err != nil {
		return err
	}
	result, err := turn.Run(ctx)
	if err != nil {
		return err
	}
	if result.Stop != agent.EndTurn {
		return fmt.Errorf("prompt stopped: %s", []string{"", "cancelled", "the model reached its token limit", "the model refused"}[result.Stop])
	}
	fmt.Println(result.Answer)
	return nil
}

// headless sends no updates. Auto mode never asks for permission.
type headless struct{}

func (headless) Send(agent.Event) error { return nil }

func (headless) Approve(context.Context, transcript.ToolCall, tools.Permission) (bool, error) {
	panic("a headless run uses Auto mode and never asks for permission")
}

func login() error {
	key, err := readKey()
	if err != nil {
		return err
	}
	if key == "" {
		return errors.New("OpenRouter API key cannot be empty")
	}
	if err := openrouter.New(key).Verify(context.Background()); err != nil {
		return err
	}
	if err := keyring.Set(key); err != nil {
		return err
	}
	fmt.Println("OpenRouter API key saved.")
	return nil
}

// readKey reads the key without echo from a terminal, or as one line from
// other input.
func readKey() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if line == "" && err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	fmt.Fprint(os.Stderr, "OpenRouter API key: ")
	key, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return strings.TrimSpace(string(key)), err
}

func logout() error {
	removed, err := keyring.Delete()
	if err != nil {
		return err
	}
	if removed {
		fmt.Println("OpenRouter API key removed.")
	} else {
		fmt.Println("No saved OpenRouter API key.")
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		fmt.Fprintln(os.Stderr, "OPENROUTER_API_KEY is still set and will continue to be used.")
	}
	return nil
}
