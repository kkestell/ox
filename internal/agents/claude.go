package agents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type Options struct {
	Workspace      string `json:"workspace"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	ready  chan error
	done   chan struct{}

	mu          sync.Mutex
	writeMu     sync.Mutex
	state       string
	prompt      *submission
	configuring bool
	control     *controlCall
}

func startClaude(options Options) (*process, error) {
	args := []string{
		"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool=stdio",
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
	return &process{
		cmd: cmd, stdin: stdin, stdout: stdout,
		ready: make(chan error, 1), done: make(chan struct{}), state: "idle",
	}, nil
}

func (p *process) run() {
	output := bufio.NewReader(p.stdout)
	p.ready <- initialize(p.stdin, output)
	p.readOutput(output)
	p.finish(p.cmd.Wait())
}

func (p *process) stop() error {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop Claude: %w", err)
	}
	<-p.done
	return nil
}

func (p *process) write(message any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return json.NewEncoder(p.stdin).Encode(message)
}

func initialize(input io.Writer, output *bufio.Reader) error {
	const request = `{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}`
	if _, err := io.WriteString(input, request+"\n"); err != nil {
		return err
	}
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read startup response: %w", err)
		}
		var message struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string `json:"request_id"`
				Subtype   string `json:"subtype"`
				Error     string `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &message) != nil || message.Type != "control_response" || message.Response.RequestID != "initialize" {
			continue
		}
		if message.Response.Subtype != "success" {
			return fmt.Errorf("Claude refused initialization (%s): %s", message.Response.Subtype, message.Response.Error)
		}
		return nil
	}
}
