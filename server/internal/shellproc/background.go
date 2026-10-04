package shellproc

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// maxProcesses is the most background processes one session retains.
const maxProcesses = 16

// stopGrace is how long an explicit stop waits after SIGTERM before SIGKILL.
const stopGrace = 2 * time.Second

// writeTimeout is the longest one write waits for the command to accept input.
const writeTimeout = 5 * time.Second

// Processes are the background processes of one session.
type Processes struct {
	mu sync.Mutex
	// closed is set when shutdown begins; no command starts afterward.
	closed bool
	// list is oldest first.
	list []*Process
}

// Process is one background command. Its supervisor goroutine owns the child
// and its process group until the command ends and the group is cleaned up.
type Process struct {
	ID      string
	Command string

	mu          sync.Mutex
	stdout      *Capture
	stderr      *Capture
	state       State
	diagnostics string

	stdinMu sync.Mutex
	stdin   *os.File

	stopOnce, killOnce sync.Once
	stop, kill         chan struct{}
	// done closes after the command ended, its group was cleaned up, and its
	// final state was published.
	done chan struct{}
}

// State is what is known about a background command.
type State struct {
	// Status is nil while the command runs.
	Status *os.ProcessState
	// Stopped reports that the command ended after an explicit stop or
	// shutdown rather than exiting on its own.
	Stopped bool
}

// Running reports whether the command is still running.
func (s State) Running() bool { return s.Status == nil }

// Output is a snapshot of a background command's state and retained output.
type Output struct {
	State          State
	Stdout, Stderr *Capture
	Diagnostics    string
}

// Start starts cmd with piped stdin, stdout, and stderr in a new process
// group, keeping up to limit bytes of each stream. When the session already
// retains the most processes, its oldest finished one is removed first. It
// fails without starting when shutdown has begun or every retained process is
// running.
func (p *Processes) Start(cmd *exec.Cmd, command string, limit int) (*Process, error) {
	// Starting under the lock means a concurrent shutdown either sees this
	// process or prevents it from starting.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("this session's shell processes are shutting down")
	}
	removable := -1
	if len(p.list) >= maxProcesses {
		for i, process := range p.list {
			if !process.State().Running() {
				removable = i
				break
			}
		}
		if removable < 0 {
			return nil, fmt.Errorf("%d shell processes are running; stop one before starting another", maxProcesses)
		}
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	process := &Process{
		ID:      rand.Text(),
		Command: command,
		stdout:  NewCapture(limit),
		stderr:  NewCapture(limit),
		stdin:   stdinW,
		stop:    make(chan struct{}),
		kill:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	out, err := start(cmd, stdinR, &process.mu, process.stdout, process.stderr)
	stdinR.Close()
	if err != nil {
		stdinW.Close()
		return nil, err
	}
	if removable >= 0 {
		p.list = append(p.list[:removable], p.list[removable+1:]...)
	}
	p.list = append(p.list, process)
	go process.supervise(cmd, out)
	return process, nil
}

// List returns every retained process, oldest first.
func (p *Processes) List() []*Process {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Process{}, p.list...)
}

// Get returns the process with the ID, or nil.
func (p *Processes) Get(id string) *Process {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, process := range p.list {
		if process.ID == id {
			return process
		}
	}
	return nil
}

// BeginShutdown closes registration and asks every process to be killed at
// once, without waiting. Repeating it is harmless.
func (p *Processes) BeginShutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, process := range p.list {
		process.requestKill()
	}
}

// Shutdown begins shutdown and waits until every command ended and its group
// was cleaned up.
func (p *Processes) Shutdown() {
	p.BeginShutdown()
	for _, process := range p.List() {
		<-process.done
	}
}

func (p *Process) requestStop() { p.stopOnce.Do(func() { close(p.stop) }) }

func (p *Process) requestKill() {
	p.requestStop()
	p.killOnce.Do(func() { close(p.kill) })
}

// supervise waits until the command exits or a stop or kill is requested,
// cleans up the whole group, drains the remaining output, and publishes the
// final state.
func (p *Process) supervise(cmd *exec.Cmd, out *output) {
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	stopped := false
	select {
	case <-exited:
	case <-p.stop:
		stopped = true
	}
	grace := time.Duration(0)
	if stopped {
		select {
		case <-p.kill:
		default:
			grace = stopGrace
		}
	}
	terminate(cmd.Process.Pid, grace, p.kill)
	<-exited
	diagnostics := out.drain()
	// A finished command cannot receive input. Wait for an in-flight write to
	// finish before publishing the final state.
	p.stdinMu.Lock()
	p.stdin.Close()
	p.stdin = nil
	p.stdinMu.Unlock()
	p.mu.Lock()
	p.state = State{Status: cmd.ProcessState, Stopped: stopped}
	p.diagnostics = diagnostics
	p.mu.Unlock()
	close(p.done)
}

// State returns the command's current state.
func (p *Process) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Output returns a snapshot of the state and retained output.
func (p *Process) Output() Output {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Output{State: p.state, Stdout: p.stdout.clone(), Stderr: p.stderr.clone(), Diagnostics: p.diagnostics}
}

// Wait waits up to limit for the command to end. It returns false when ctx
// is done first.
func (p *Process) Wait(ctx context.Context, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-p.done:
	case <-timer.C:
	}
	return true
}

// Stop asks the supervisor to stop the command and waits for its bounded
// cleanup. A finished command is not signalled.
func (p *Process) Stop() Output {
	p.requestStop()
	<-p.done
	return p.Output()
}

// Interruption is why a write stopped before sending all of its text.
type Interruption int

const (
	NotInterrupted Interruption = iota
	StdinClosed
	Finished
	WriteTimedOut
	WriteCancelled
	WriteFailed
)

// Written is what one write accomplished.
type Written struct {
	Bytes        int
	StdinClosed  bool
	Interruption Interruption
	// Error describes a failed write.
	Error string
}

// Write sends text to stdin within writeTimeout, then closes stdin when
// closeStdin is set and all of the text was written. A failed write closes
// stdin; a timeout or cancellation leaves it open.
func (p *Process) Write(ctx context.Context, text []byte, closeStdin bool) Written {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if !p.State().Running() {
		return Written{StdinClosed: p.stdin == nil, Interruption: Finished}
	}
	if p.stdin == nil {
		return Written{StdinClosed: true, Interruption: StdinClosed}
	}
	stdin := p.stdin
	stdin.SetWriteDeadline(time.Now().Add(writeTimeout))
	var cancelled atomic.Bool
	stopWatching := context.AfterFunc(ctx, func() {
		cancelled.Store(true)
		stdin.SetWriteDeadline(time.Unix(1, 0))
	})
	n, err := stdin.Write(text)
	stopWatching()
	stdin.SetWriteDeadline(time.Time{})
	written := Written{Bytes: n}
	switch {
	case err == nil:
	case errors.Is(err, os.ErrDeadlineExceeded) && cancelled.Load():
		written.Interruption = WriteCancelled
	case errors.Is(err, os.ErrDeadlineExceeded):
		written.Interruption = WriteTimedOut
	default:
		written.Interruption = WriteFailed
		written.Error = err.Error()
	}
	if written.Interruption == WriteFailed || (written.Interruption == NotInterrupted && closeStdin) {
		stdin.Close()
		p.stdin = nil
	}
	written.StdinClosed = p.stdin == nil
	return written
}
