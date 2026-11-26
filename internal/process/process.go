// Package process runs a harness's process on pipes Ox owns: it delivers each
// line of the process's stdout as an event, logs its stderr, and delivers its
// exit after its last line.
package process

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/children"
)

// Process is a started harness process.
type Process struct {
	cmd    *exec.Cmd
	output *os.File      // the read end of the process's stdout
	stderr *os.File      // the read end of the process's stderr
	reaped chan struct{} // closed once the process has exited and been reaped
}

// Exit is the process's exit, delivered after its last line of output.
type Exit struct {
	Err    error
	Stderr string // the end of its stderr, trimmed
}

// Start starts cmd and delivers parse's event for each nonblank line of its
// stdout, then its Exit, on events until done is closed. name, the program,
// and logID, the session's ID, name each line of its stderr in the log.
// children.KillAll sends the process kill. The returned writer is its stdin.
func Start(cmd *exec.Cmd, name, logID string, kill syscall.Signal, parse func([]byte) agent.Event, events chan<- agent.Event, done <-chan struct{}) (*Process, io.WriteCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	// Ox owns the output pipes, unlike StdoutPipe's, so Wait neither waits for
	// them nor closes them: reading stops once the process has exited and its
	// output has drained, even while a child that inherited a pipe holds it
	// open.
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, nil, err
	}
	stderr, childStderr, err := os.Pipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		childStdout.Close()
		return nil, nil, err
	}
	cmd.Stdout = childStdout
	cmd.Stderr = childStderr
	err = children.Start(cmd, func() { cmd.Process.Signal(kill) })
	childStdout.Close()
	childStderr.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, nil, err
	}
	stderrTail := make(chan string, 1)
	go logStderr(stderr, name, logID, stderrTail)
	p := &Process{cmd: cmd, output: stdout, stderr: stderr, reaped: make(chan struct{})}
	captured := make(chan agent.Event)
	go p.read(parse, stderrTail, captured)
	go deliverOutput(captured, events, done)
	return p, stdin, nil
}

// stderrKept is how much of the process's stderr its Exit reports.
const stderrKept = 2048

// logStderr logs each line of stderr as name's under logID until the pipe
// closes or its read deadline passes, then closes it and sends the last of it.
func logStderr(stderr *os.File, name, logID string, tail chan<- string) {
	var last, line []byte
	cut := false
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		line = append(line, buf[:n]...)
		for {
			i := bytes.IndexByte(line, '\n')
			if i < 0 {
				break
			}
			log.Printf("session %s: %s: %s", logID, name, line[:i])
			line = line[i+1:]
		}
		if err != nil && len(line) > 0 {
			log.Printf("session %s: %s: %s", logID, name, line)
		}
		last = append(last, buf[:n]...)
		if len(last) > stderrKept {
			last, cut = last[len(last)-stderrKept:], true
		}
		if err != nil {
			break
		}
	}
	stderr.Close()
	text := strings.TrimSpace(string(last))
	// Drop the line whose start was cut, unless it is the only one.
	if i := strings.IndexByte(text, '\n'); cut && i >= 0 {
		text = text[i+1:]
	}
	tail <- text
}

// read drains the process's output, from spawn to exit, into the delivery
// queue, then records its exit. A second after the process exits, the pipe
// reads stop even if an inherited descriptor is open; delivery from the queue
// is independent of that limit.
func (p *Process) read(parse func([]byte) agent.Event, stderrTail <-chan string, captured chan<- agent.Event) {
	defer close(captured)
	exited := make(chan error, 1)
	go func() {
		err := p.cmd.Wait()
		children.Done(p.cmd)
		close(p.reaped)
		p.output.SetReadDeadline(time.Now().Add(time.Second))
		p.stderr.SetReadDeadline(time.Now().Add(time.Second))
		exited <- err
	}()
	output := bufio.NewReader(p.output)
	for {
		// A line that ends at EOF is still a line.
		line, err := output.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			captured <- parse(line)
		}
		if err != nil {
			break
		}
	}
	p.output.Close()
	captured <- Exit{Err: <-exited, Stderr: <-stderrTail}
}

// deliverOutput never makes the reader wait for the session, which may be
// writing to the process's stdin while the process writes stdout. Once done is
// closed, the agent has ended and its output is discarded.
func deliverOutput(captured <-chan agent.Event, events chan<- agent.Event, done <-chan struct{}) {
	var queue []agent.Event
	for captured != nil || len(queue) != 0 {
		var next agent.Event
		var ready chan<- agent.Event
		if len(queue) != 0 {
			next, ready = queue[0], events
		}
		select {
		case event, ok := <-captured:
			if ok {
				queue = append(queue, event)
			} else {
				captured = nil
			}
		case ready <- next:
			queue[0] = nil
			queue = queue[1:]
		case <-done:
			if captured != nil {
				for range captured {
				}
			}
			return
		}
	}
}

// Terminate asks the process to exit so it can clean up its tool processes,
// and kills it if it is still running after after, or at once if it cannot be
// asked.
func (p *Process) Terminate(after time.Duration) {
	switch err := p.cmd.Process.Signal(syscall.SIGTERM); {
	case err == nil:
		time.AfterFunc(after, func() { p.cmd.Process.Kill() })
	case !errors.Is(err, os.ErrProcessDone):
		p.cmd.Process.Kill()
	}
}

// Reaped is closed once the process has exited and been reaped.
func (p *Process) Reaped() <-chan struct{} {
	return p.reaped
}
