package claude

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/process"
)

// systemPrompt is the instructions appended to Claude's system prompt: Ox's
// style, then what Claude needs to know about running as Ox session
// oxSessionID in workspace.
func systemPrompt(oxSessionID, workspace string) string {
	return agent.Style + "\n\n" +
		"You are running inside Ox, which runs several Claude Code sessions side by side. " +
		"You are Ox session `" + oxSessionID + "` in `" + workspace + "`. The user talks to you through Ox.\n\n" +
		"\"Another session\" or \"the other agent\" means another Ox session. " +
		"Find them with `list_sessions` and message them with `send_message`, both from the `ox` MCP server. " +
		"Use no other messaging tool. " +
		"To hand work to another agent, start a child session with `create_session` from the same server.\n\n" +
		agent.Sessions
}

// efforts are the effort levels Claude takes. Given any other, it runs at its
// default effort with only a warning.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// checkEffort reports whether Claude takes effort; an empty effort leaves its
// default.
func checkEffort(effort string) error {
	if effort != "" && !slices.Contains(efforts, effort) {
		return fmt.Errorf("Claude has no effort level %q", effort)
	}
	return nil
}

// permissionModes are the permission modes Claude takes, other than its alias
// default for manual.
var permissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}

// checkPermissionMode refuses default, Claude's alias for manual, so that one
// mode has one ID, and any mode Claude does not take.
func checkPermissionMode(mode string) error {
	if mode == "default" {
		return errors.New("use permission mode \"manual\" instead of \"default\" for Claude")
	}
	if !slices.Contains(permissionModes, mode) {
		return fmt.Errorf("Claude has no permission mode %q", mode)
	}
	return nil
}

// startProcess starts Claude for the conversation sessionID, with Ox's MCP
// server registered as ox, and delivers its output and exit on events until
// done is closed. oxSessionID is the Ox session's ID: Claude's instructions
// and the log of its stderr name it. With thinkingSummaries, Claude streams a
// summary of its thinking; without it, thinking deltas arrive empty.
func startProcess(workspace, oxSessionID, sessionID string, resume, thinkingSummaries bool, settings agent.Settings, mcp agent.MCPServer, events chan<- agent.Event, done <-chan struct{}) (*process.Process, *writer, error) {
	if err := checkEffort(settings.Effort); err != nil {
		return nil, nil, err
	}
	// Strings always marshal.
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"ox": map[string]any{"command": mcp.Command, "args": []string{}, "env": mcp.Env},
	}})
	approved := make([]string, len(mcp.Approved))
	for i, tool := range mcp.Approved {
		approved[i] = "mcp__ox__" + tool
	}
	args := []string{
		"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool=stdio",
		"--append-system-prompt", systemPrompt(oxSessionID, workspace), "--system-prompt-snapshot=off",
	}
	if thinkingSummaries {
		args = append(args, "--thinking-display=summarized")
	}
	if resume {
		args = append(args, "--resume="+sessionID)
	} else {
		args = append(args, "--session-id="+sessionID)
	}
	args = append(args, "--model="+settings.Model, "--permission-mode="+settings.PermissionMode)
	if settings.Effort != "" {
		args = append(args, "--effort="+settings.Effort)
	}
	// Without --strict-mcp-config the user's own MCP servers still load. The
	// = form keeps the variadic --allowedTools from taking later arguments.
	args = append(args, "--mcp-config", string(mcpConfig), "--allowedTools="+strings.Join(approved, ","))
	// Claude Code's own cross-session messaging reaches every local Claude
	// session, so an Ox session would message and be messaged outside Ox. Ox's
	// sessions message only through Ox, hand work to another agent by starting
	// an Ox child session instead of a subagent, and work in the worktree Ox
	// gives them instead of one Claude Code creates.
	args = append(args, "--disallowedTools=SendMessage,ListAgents,Agent,EnterWorktree,ExitWorktree", `--settings={"crossSessionInbound":"refuse"}`)

	cmd := exec.Command("claude", args...)
	cmd.Dir = workspace
	cmd.Env = append(launchEnv(cmd), "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1")
	p, stdin, err := process.Start(cmd, "claude", oxSessionID, syscall.SIGKILL, parse, nil, events, done)
	if err != nil {
		return nil, nil, err
	}
	return p, newWriter(stdin), nil
}

// parse reads a line of Claude's stdout: a message, or plain text, such as a
// startup failure.
func parse(line []byte) agent.Event {
	var m message
	if json.Unmarshal(line, &m) == nil {
		return m
	}
	return plainText(strings.TrimSpace(string(line)))
}

// overrides are the variables, beyond the ANTHROPIC_ prefix, that run Claude
// with another effort, provider, or account than Ox chooses.
var overrides = []string{
	// effort: beats --effort and the apply_flag_settings effort change
	"CLAUDE_CODE_EFFORT_LEVEL",
	// provider: sends Claude to another provider's billing
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_MANTLE", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD",
	"CLAUDE_CODE_USE_GATEWAY", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "CLAUDE_CODE_API_BASE_URL",
	// account: an API key, billed instead of the subscription
	"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR",
}

// launchEnv is cmd's environment without anything that would run Claude with
// another effort, model, provider, or account than Ox chooses: every
// ANTHROPIC_ variable and each of overrides. The user's subscription login
// (CLAUDE_CONFIG_DIR, CLAUDE_CODE_OAUTH_TOKEN) stays.
func launchEnv(cmd *exec.Cmd) []string {
	return slices.DeleteFunc(cmd.Environ(), func(v string) bool {
		name, _, _ := strings.Cut(v, "=")
		return strings.HasPrefix(name, "ANTHROPIC_") || slices.Contains(overrides, name)
	})
}

// plainText is a line of Claude's stdout that is not JSON, such as a
// startup failure.
type plainText string

// killAfter is how long Claude has to exit once asked before it is killed.
var killAfter = 10 * time.Second

// startupLimit is how long Claude has to answer initialize.
var startupLimit = time.Minute

// writer writes messages to Claude's stdin. It is not safe for concurrent use.
type writer struct {
	w io.Writer
}

func newWriter(w io.Writer) *writer {
	return &writer{w: w}
}

func (in *writer) write(message any) error {
	return json.NewEncoder(in.w).Encode(message)
}

// Prompt sends a user message. Claude reports the uuid in command_lifecycle
// messages once it takes the prompt into its conversation. A prompt with
// images is content blocks: the images first, since Claude reads an image best
// before the text that refers to it, then the text when it is not blank.
func (in *writer) Prompt(uuid, text string, images []agent.Image) error {
	var content any = text
	if len(images) > 0 {
		blocks := []map[string]any{}
		for _, image := range images {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]string{
					"type": "base64", "media_type": image.MimeType, "data": base64.StdEncoding.EncodeToString(image.Data),
				},
			})
		}
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
		content = blocks
	}
	return in.write(map[string]any{
		"type": "user", "uuid": uuid, "session_id": "", "priority": "next",
		"message": map[string]any{"role": "user", "content": content},
	})
}

// Control sends a control request. Claude answers with a control_response
// carrying the same request ID.
func (in *writer) Control(requestID string, request any) error {
	return in.write(map[string]any{
		"type": "control_request", "request_id": requestID, "request": request,
	})
}

func (in *writer) Respond(requestID string, response any) error {
	return in.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": response,
		},
	})
}

func (in *writer) Reject(requestID, message string) error {
	return in.write(map[string]any{
		"type": "control_response",
		"response": map[string]string{
			"subtype": "error", "request_id": requestID, "error": message,
		},
	})
}

// newUUID returns a random UUID, the form Claude requires for session IDs.
func newUUID() string {
	var id [16]byte
	rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
