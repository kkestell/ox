package agents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

type Options struct {
	Workspace      string `json:"workspace"`
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

// startClaude starts a Claude process and writes its initialize request.
// The outcome of initialization is sent to ready.
func startClaude(options Options, ready chan error) (*process, error) {
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
	const initialize = `{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}`
	if _, err := io.WriteString(stdin, initialize+"\n"); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	return &process{
		cmd: cmd, stdin: stdin, stdout: stdout,
		events: make(chan any), done: make(chan struct{}),
		phase: starting, claudeState: "idle", startReply: ready,
	}, nil
}

// read turns Claude's output into events, ending with processExited.
func (p *process) read() {
	output := bufio.NewReader(p.stdout)
	for {
		line, err := output.ReadBytes('\n')
		if err != nil {
			break
		}
		var message claudeOutput
		if json.Unmarshal(line, &message) == nil {
			p.events <- message
		}
	}
	p.events <- processExited{p.cmd.Wait()}
}

func (p *process) stop() error {
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop Claude: %w", err)
	}
	<-p.done
	return nil
}

func (p *process) write(message any) error {
	return json.NewEncoder(p.stdin).Encode(message)
}
