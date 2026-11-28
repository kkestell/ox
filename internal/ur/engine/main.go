// Package engine is the ur program, Ox's built-in harness, which chats through
// OpenRouter's chat completions API and runs the model's shell commands. oxd
// drives it over stdin and stdout with the wire protocol. Each turn is a
// streamed request carrying Ur's instructions and then the conversation so
// far, which ur keeps in a file of its own, then a request after each
// response's calls have been answered. Given Ox's MCP forwarder, ur also
// offers Ox's tools, which it calls through the forwarder.
package engine

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkestell/ox/internal/agent"
)

// Endpoint is OpenRouter's chat completions API.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// Main runs ur with args, the command line without the program name, and
// returns its exit status.
//
// Agent mode, run in the session's workspace:
//
//	ur -dir <conversations> [-resume <token>] -model <id> [-effort <e>] -permission-mode <m> [-providers <json>] [-endpoint <url>] [-mcp <json> -session-id <id>]
//
// reads Commands on stdin and writes Events on stdout until stdin ends or
// SIGTERM. With -mcp, an agent.MCPServer as JSON, it starts the forwarder
// and offers Ox's tools as session -session-id. Ask mode:
//
//	ur -ask -model <id> [-effort <e>] -instructions <text> [-providers <json>] [-endpoint <url>]
//
// sends the text on stdin and prints the reply.
func Main(args []string) int {
	// A Ctrl-C in oxd's terminal reaches ur only as oxd's orderly stop. A
	// write to an oxd that has gone fails instead of killing ur before it has
	// killed its commands. The signals are handled rather than ignored, so
	// each command starts with their defaults.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGINT, syscall.SIGPIPE)
	return run(args, os.Stdin, os.Stdout, os.Stderr)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ur", flag.ContinueOnError)
	flags.SetOutput(stderr)
	askMode := flags.Bool("ask", false, "send the text on stdin once and print the reply")
	dir := flags.String("dir", "", "the directory of conversation files")
	resume := flags.String("resume", "", "the token of the conversation to resume")
	model := flags.String("model", "", "the model's ID")
	effort := flags.String("effort", "", "the reasoning effort")
	mode := flags.String("permission-mode", "manual", "manual or auto")
	system := flags.String("instructions", "", "ask mode's system message")
	providersJSON := flags.String("providers", "", "a JSON object from model ID to provider order")
	endpoint := flags.String("endpoint", Endpoint, "the chat completions endpoint")
	mcpJSON := flags.String("mcp", "", "Ox's MCP server for the session, as JSON")
	sessionID := flags.String("session-id", "", "the Ox session ID, which -mcp acts for")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	permissionMode, err := agent.ParsePermissionMode(*mode)
	if err != nil {
		fmt.Fprintf(stderr, "-permission-mode: %v\n", err)
		return 2
	}
	c := client{endpoint: *endpoint}
	if *providersJSON != "" {
		if err := json.Unmarshal([]byte(*providersJSON), &c.providers); err != nil {
			fmt.Fprintf(stderr, "-providers: %v\n", err)
			return 2
		}
	}
	c.key = os.Getenv("OPENROUTER_API_KEY")
	if c.key == "" {
		fmt.Fprintln(stderr, "OPENROUTER_API_KEY is not set")
		return 1
	}
	if *askMode {
		text, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		reply, err := c.ask(context.Background(), agent.Settings{Model: *model, Effort: *effort}, *system, string(text))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprint(stdout, reply)
		return 0
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "-dir is required")
		return 2
	}
	var mcp *agent.MCPServer
	if *mcpJSON != "" {
		mcp = new(agent.MCPServer)
		if err := json.Unmarshal([]byte(*mcpJSON), mcp); err != nil || mcp.Command == "" {
			fmt.Fprintf(stderr, "-mcp: %q is not an MCP server with a command\n", *mcpJSON)
			return 2
		}
		if *sessionID == "" {
			fmt.Fprintln(stderr, "-session-id is required with -mcp")
			return 2
		}
	}
	workspace, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	e := &engine{
		client: c, workspace: workspace, out: json.NewEncoder(stdout), stderr: stderr,
		events: make(chan event), stopped: make(chan struct{}),
		settings: agent.Settings{Model: *model, Effort: *effort, PermissionMode: permissionMode},
	}
	return e.serve(stdin, *dir, *resume, mcp, *sessionID)
}
