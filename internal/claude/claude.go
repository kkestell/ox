// Package claude runs a Claude Code process in stream-json mode. It owns the
// subprocess and Claude's wire format: Process starts and initializes Claude
// and delivers its output as Messages, and Input writes Claude's input
// messages. It knows nothing about agents or clients.
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
)

type Options struct {
	Workspace      string
	SessionID      string // a UUID naming Claude's conversation
	Resume         bool   // continue the conversation's transcript instead of starting it
	Model          string
	Effort         string
	PermissionMode string
}

type Process struct {
	*Input
	cmd    *exec.Cmd
	output chan Message
	err    error
}

// Start starts Claude and waits up to one minute for it to accept
// initialization.
func Start(options Options) (*Process, error) {
	args := []string{
		"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool=stdio",
	}
	if options.Resume {
		args = append(args, "--resume="+options.SessionID)
	} else {
		args = append(args, "--session-id="+options.SessionID)
	}
	if options.Model != "" {
		args = append(args, "--model="+options.Model)
	}
	if options.Effort != "" {
		args = append(args, "--effort="+options.Effort)
	}
	if options.PermissionMode != "" {
		args = append(args, "--permission-mode="+options.PermissionMode)
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = options.Workspace
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

	p := &Process{Input: NewInput(stdin), cmd: cmd, output: make(chan Message)}
	go p.read(output)
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

func (p *Process) read(output *bufio.Reader) {
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			break
		}
		var message Message
		if json.Unmarshal(line, &message) == nil {
			p.output <- message
		}
	}
	p.err = p.cmd.Wait()
	close(p.output)
}

// Output delivers Claude's messages and is closed when the process exits.
func (p *Process) Output() <-chan Message {
	return p.output
}

// Err reports how the process exited. It is valid once Output is closed.
func (p *Process) Err() error {
	return p.err
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
