// Package claude is the Claude Code harness. Start runs a Claude Code process
// in stream-json mode as an agent.Agent: Process owns the subprocess, Message
// and Input are Claude's wire format, and Agent is the state machine that
// turns them into prompts, approvals, configuration, and updates.
package claude

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type Process struct {
	*Input
	cmd *exec.Cmd
}

// startProcess starts Claude in workspace on the conversation named by
// sessionID, a UUID, and waits up to one minute for it to accept
// initialization. resume continues the conversation's transcript instead of
// starting it. Claude's output then goes to events, followed by its exit.
func startProcess(workspace, sessionID string, resume bool, settings agent.Settings, events chan<- agent.Event) (*Process, error) {
	args := []string{
		"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool=stdio",
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

	cmd := exec.Command("claude", args...)
	cmd.Dir = workspace
	cmd.Env = append(cmd.Environ(), "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	output := bufio.NewReader(stdout)
	timeout := time.AfterFunc(time.Minute, func() { cmd.Process.Kill() })
	err = initialize(stdin, output)
	if !timeout.Stop() {
		err = errors.New("no startup response within one minute")
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("initialize Claude: %w", err)
	}

	p := &Process{Input: NewInput(stdin), cmd: cmd}
	go p.read(output, events)
	return p, nil
}

func initialize(input io.Writer, output *bufio.Reader) error {
	// The Stop hook reports Claude's pending schedules at the end of each turn.
	const request = `{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize","hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}}}`
	if _, err := io.WriteString(input, request+"\n"); err != nil {
		return err
	}
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read startup response: %w", err)
		}
		var message Message
		if json.Unmarshal(line, &message) != nil || message.Type != "control_response" || message.Response.RequestID != "initialize" {
			continue
		}
		if message.Response.Subtype != "success" {
			return fmt.Errorf("Claude refused initialization (%s): %s", message.Response.Subtype, message.Response.Error)
		}
		return nil
	}
}

// read sends each of Claude's messages to events, then how the process
// exited.
func (p *Process) read(output *bufio.Reader, events chan<- agent.Event) {
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			break
		}
		var message Message
		if json.Unmarshal(line, &message) == nil {
			events <- message
		}
	}
	events <- processExit{p.cmd.Wait()}
}

// Terminate asks Claude to exit so it can clean up its tool processes.
func (p *Process) Terminate() error {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop Claude: %w", err)
	}
	return nil
}

// Input writes messages to Claude's stdin. It is not safe for concurrent use.
type Input struct {
	w io.Writer
}

func NewInput(w io.Writer) *Input {
	return &Input{w: w}
}

func (in *Input) write(message any) error {
	return json.NewEncoder(in.w).Encode(message)
}

// Prompt sends a user message. Claude reports the uuid in command_lifecycle
// messages once it takes the prompt into its conversation.
func (in *Input) Prompt(uuid, text string) error {
	return in.write(map[string]any{
		"type": "user", "uuid": uuid, "session_id": "",
		"message": map[string]string{"role": "user", "content": text},
	})
}

// Control sends a control request. Claude answers with a control_response
// carrying the same request ID.
func (in *Input) Control(requestID string, request any) error {
	return in.write(map[string]any{
		"type": "control_request", "request_id": requestID, "request": request,
	})
}

// Respond answers one of Claude's control requests.
func (in *Input) Respond(requestID string, response any) error {
	return in.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": response,
		},
	})
}

// Reject refuses one of Claude's control requests.
func (in *Input) Reject(requestID, message string) error {
	return in.write(map[string]any{
		"type": "control_response",
		"response": map[string]string{
			"subtype": "error", "request_id": requestID, "error": message,
		},
	})
}

// NewUUID returns a random UUID, the form Claude requires for session IDs.
func NewUUID() string {
	var id [16]byte
	rand.Read(id[:])
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
