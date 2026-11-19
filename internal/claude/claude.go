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
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type process struct {
	cmd    *exec.Cmd
	output *os.File // the read end of Claude's stdout
}

const systemInstructions = "Lead with the big idea up front. " +
	"Use crisp, deadpan, military-efficient delivery. " +
	"Use dry wit with complete emotional neutrality."

// efforts are the effort levels Claude takes. Given any other, it runs at its
// default effort with only a warning.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// CheckEffort reports whether Claude takes effort; an empty effort leaves its
// default.
func CheckEffort(effort string) error {
	if effort != "" && !slices.Contains(efforts, effort) {
		return fmt.Errorf("Claude has no effort level %q", effort)
	}
	return nil
}

func startProcess(workspace, sessionID string, resume bool, settings agent.Settings, events chan<- agent.Event) (*process, *writer, error) {
	if err := CheckEffort(settings.Effort); err != nil {
		return nil, nil, err
	}
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
	// Ox owns the stdout pipe, unlike StdoutPipe's, so Wait neither waits for
	// it nor closes it: read stops once Claude has exited and its output has
	// drained, even while a child that inherited the pipe holds it open.
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, nil, err
	}
	cmd.Stdout = childStdout
	err = cmd.Start()
	childStdout.Close()
	if err != nil {
		stdout.Close()
		return nil, nil, err
	}

	output := bufio.NewReader(stdout)
	// The deadline ends the read even when a child of Claude holds stdout.
	timeout := time.AfterFunc(time.Minute, func() {
		cmd.Process.Kill()
		stdout.SetReadDeadline(time.Now())
	})
	err = initialize(stdin, output)
	if !timeout.Stop() {
		err = errors.New("no startup response within one minute")
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		stdout.Close()
		return nil, nil, fmt.Errorf("initialize Claude: %w", err)
	}

	p := &process{cmd: cmd, output: stdout}
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
		// A line that ends at EOF is still a line.
		line, err := output.ReadBytes('\n')
		var m message
		switch {
		case strings.TrimSpace(string(line)) == "":
		case json.Unmarshal(line, &m) != nil:
			reason = strings.TrimSpace(string(line))
		case m.Type == "result" && m.failed():
			reason = m.errorText()
		case m.Type == "control_response" && m.Response.RequestID == "initialize":
			if m.Response.Subtype != "success" {
				return fmt.Errorf("Claude refused initialization (%s): %s", m.Response.Subtype, m.Response.Error)
			}
			return nil
		}
		if err != nil {
			switch {
			case reason != "":
				return errors.New(reason)
			case writeErr != nil:
				return writeErr
			}
			return fmt.Errorf("read startup response: %w", err)
		}
	}
}

// read delivers Claude's output, then its exit. Output Claude wrote before
// exiting is already in the pipe; a second after the exit, reading stops even
// if a child still holds the pipe open.
func (p *process) read(output *bufio.Reader, events chan<- agent.Event) {
	exited := make(chan error, 1)
	go func() {
		err := p.cmd.Wait()
		p.output.SetReadDeadline(time.Now().Add(time.Second))
		exited <- err
	}()
	for {
		// A line that ends at EOF is still a line.
		line, err := output.ReadBytes('\n')
		var m message
		if json.Unmarshal(line, &m) == nil {
			events <- m
		}
		if err != nil {
			break
		}
	}
	p.output.Close()
	events <- processExit{<-exited}
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
