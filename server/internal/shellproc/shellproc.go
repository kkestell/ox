// Package shellproc runs commands in their own process groups: bounded output
// capture, cleanup of the whole group, foreground runs with a deadline and
// cancellation, and the background processes of one session.
package shellproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// drainTimeout bounds how long output is read after a group is cleaned up. A
// detached descendant can hold a pipe open indefinitely.
const drainTimeout = time.Second

const groupPollInterval = 10 * time.Millisecond

// Capture keeps the start and end of one output stream, at most limit bytes
// together.
type Capture struct {
	limit int
	head  []byte
	tail  []byte
	// Omitted reports that output between head and tail was dropped.
	Omitted bool
	// Total counts every byte the stream produced, kept or dropped.
	Total uint64
}

// NewCapture returns an empty capture of at most limit bytes.
func NewCapture(limit int) *Capture {
	return &Capture{limit: limit}
}

// Write keeps the first half of the limit and the latest rest of the stream.
func (c *Capture) Write(p []byte) (int, error) {
	c.Total += uint64(len(p))
	headLimit := c.limit / 2
	take := min(max(headLimit-len(c.head), 0), len(p))
	c.head = append(c.head, p[:take]...)
	rest := p[take:]
	excess := len(c.tail) + len(rest) - (c.limit - headLimit)
	if excess > 0 {
		c.Omitted = true
		dropped := min(excess, len(c.tail))
		c.tail = append(c.tail[dropped:len(c.tail):len(c.tail)], rest[excess-dropped:]...)
	} else {
		c.tail = append(c.tail, rest...)
	}
	return len(p), nil
}

// Text returns the stream's text, or its start and end when the middle was
// omitted. Invalid UTF-8 becomes U+FFFD.
func (c *Capture) Text() (head, tail string) {
	if c.Omitted {
		return lossy(c.head), lossy(c.tail)
	}
	return lossy(append(append([]byte{}, c.head...), c.tail...)), ""
}

func (c *Capture) clone() *Capture {
	clone := *c
	clone.head = append([]byte{}, c.head...)
	clone.tail = append([]byte{}, c.tail...)
	return &clone
}

func lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out []rune
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		out = append(out, r)
		b = b[size:]
	}
	return string(out)
}

// output reads a child's stdout and stderr into captures.
type output struct {
	// mu guards the captures while they are being written.
	mu             *sync.Mutex
	stdout, stderr *Capture
	files          []*os.File
	done           chan struct{}
	errors         []string
}

// start starts the command in a new process group with its output piped into
// the captures. stdin, when not nil, becomes the child's stdin.
func start(cmd *exec.Cmd, stdin *os.File, mu *sync.Mutex, stdout, stderr *Capture) (*output, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	if stdin != nil {
		cmd.Stdin = stdin
	}
	err = cmd.Start()
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, err
	}
	o := &output{mu: mu, stdout: stdout, stderr: stderr, files: []*os.File{outR, errR}, done: make(chan struct{})}
	var readers sync.WaitGroup
	for i, name := range []string{"stdout", "stderr"} {
		capture := []*Capture{stdout, stderr}[i]
		readers.Go(func() { o.read(o.files[i], name, capture) })
	}
	go func() {
		readers.Wait()
		close(o.done)
	}()
	return o, nil
}

func (o *output) read(file *os.File, name string, capture *Capture) {
	buffer := make([]byte, 8192)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			o.mu.Lock()
			capture.Write(buffer[:n])
			o.mu.Unlock()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				o.mu.Lock()
				o.errors = append(o.errors, fmt.Sprintf("Reading %s failed: %v", name, err))
				o.mu.Unlock()
			}
			return
		}
	}
}

// drain waits up to drainTimeout for both pipes to reach EOF, then closes
// them, and returns the capture problems as diagnostics.
func (o *output) drain() string {
	var lines []string
	select {
	case <-o.done:
	case <-time.After(drainTimeout):
		lines = append(lines, "Output capture stopped before EOF; additional output may be missing.")
	}
	for _, file := range o.files {
		file.Close()
	}
	<-o.done
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(append(lines, o.errors...), "\n")
}

// signalGroup signals a process group. An empty group, or one whose remaining
// members Ox may not signal, is not an error.
func signalGroup(group int, signal syscall.Signal) {
	err := syscall.Kill(-group, signal)
	if err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		panic(fmt.Sprintf("failed to signal owned process group: %v", err))
	}
}

func groupEmpty(group int) bool {
	return errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
}

// terminate sends SIGTERM and waits up to grace for the group to exit, or
// until kill closes, then sends SIGKILL. A zero grace sends SIGKILL at once.
func terminate(group int, grace time.Duration, kill <-chan struct{}) {
	if grace > 0 {
		signalGroup(group, syscall.SIGTERM)
		deadline := time.After(grace)
		ticker := time.NewTicker(groupPollInterval)
		defer ticker.Stop()
	wait:
		for !groupEmpty(group) {
			select {
			case <-kill:
				break wait
			case <-deadline:
				break wait
			case <-ticker.C:
			}
		}
	}
	signalGroup(group, syscall.SIGKILL)
}

// Outcome is what ended a foreground run.
type Outcome int

const (
	Exited Outcome = iota
	TimedOut
	Cancelled
)

// Result is what a foreground run observed after its group was cleaned up.
type Result struct {
	Outcome Outcome
	// Status is the child's exit status.
	Status         *os.ProcessState
	Stdout, Stderr *Capture
	// Diagnostics lists output capture problems.
	Diagnostics string
}

// Run starts cmd with stdin from /dev/null in a new process group and
// captures up to limit bytes of each stream until the child exits, timeout
// passes, or ctx is done. The whole group is then killed, even after a normal
// exit, because the child may leave background processes. It fails only when
// the child cannot start.
func Run(ctx context.Context, cmd *exec.Cmd, limit int, timeout time.Duration) (Result, error) {
	result := Result{Stdout: NewCapture(limit), Stderr: NewCapture(limit)}
	out, err := start(cmd, nil, &sync.Mutex{}, result.Stdout, result.Stderr)
	if err != nil {
		return Result{}, err
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		result.Outcome = TimedOut
	case <-ctx.Done():
		result.Outcome = Cancelled
	}
	// An exit that raced the deadline or cancellation still counts as an exit.
	select {
	case <-exited:
		result.Outcome = Exited
	default:
	}
	signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	<-exited
	result.Status = cmd.ProcessState
	result.Diagnostics = out.drain()
	return result, nil
}

// ExitText describes an exit status the way the shell tool reports it.
func ExitText(state *os.ProcessState) string {
	status := state.Sys().(syscall.WaitStatus)
	if status.Signaled() {
		return fmt.Sprintf("Terminated by signal: %d", status.Signal())
	}
	return fmt.Sprintf("Exit code: %d", status.ExitStatus())
}
