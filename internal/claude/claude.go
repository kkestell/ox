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
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type process struct {
	cmd *exec.Cmd
}

const systemInstructions = "Lead with the big idea up front. " +
	"Use crisp, deadpan, military-efficient delivery. " +
	"Use dry wit with complete emotional neutrality."

func startProcess(workspace, sessionID string, resume bool, settings agent.Settings, events chan<- agent.Event) (*process, *writer, error) {
	args := []string{
		"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool=stdio",
		"--append-system-prompt", systemInstructions, "--system-prompt-snapshot=off",
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
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("initialize Claude: %w", err)
	}

	p := &process{cmd: cmd}
	go p.read(output, events)
	return p, newWriter(stdin), nil
}

// initialize sends Claude's handshake and waits for its answer. Claude can
// fail at startup with an error result or plain text and exit, sometimes
// before it reads the handshake; the last such output is the error.
func initialize(input io.Writer, output *bufio.Reader) error {
	// Declare Stop to receive pending schedules at the end of each completed turn.
	const request = `{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize","hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}}}`
	_, writeErr := io.WriteString(input, request+"\n")
	var reason string
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			switch {
			case reason != "":
				return errors.New(reason)
			case writeErr != nil:
				return writeErr
			}
			return fmt.Errorf("read startup response: %w", err)
		}
		var m message
		if json.Unmarshal(line, &m) != nil {
			if text := strings.TrimSpace(string(line)); text != "" {
				reason = text
			}
			continue
		}
		if m.Type == "result" && m.failed() {
			reason = m.errorText()
			continue
		}
		if m.Type != "control_response" || m.Response.RequestID != "initialize" {
			continue
		}
		if m.Response.Subtype != "success" {
			return fmt.Errorf("Claude refused initialization (%s): %s", m.Response.Subtype, m.Response.Error)
		}
		return nil
	}
}

func (p *process) read(output *bufio.Reader, events chan<- agent.Event) {
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			break
		}
		var m message
		if json.Unmarshal(line, &m) == nil {
			events <- m
		}
	}
	events <- processExit{p.cmd.Wait()}
}

// Terminate asks Claude to exit so it can clean up its tool processes.
func (p *process) Terminate() error {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop Claude: %w", err)
	}
	return nil
}

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
// messages once it takes the prompt into its conversation.
func (in *writer) Prompt(uuid, text string) error {
	return in.write(map[string]any{
		"type": "user", "uuid": uuid, "session_id": "",
		"message": map[string]string{"role": "user", "content": text},
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
