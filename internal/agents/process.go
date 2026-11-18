package agents

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
)

// phase is the daemon's ownership of a Claude process. Claude's reported
// session state is tracked separately in process.claudeState.
type phase int

const (
	starting    phase = iota // initialize request written; waiting for Claude to accept it
	available                // nothing owns the process
	queued                   // prompt written; Claude has not taken it yet
	prompting                // Claude took the prompt; its next idle state releases it
	configuring              // control requests in flight, one at a time
	exited
)

// process is a state machine driven by events from clients and from Claude's
// output. The run goroutine owns every field below events and handles one
// event at a time.
type process struct {
	cmd    *exec.Cmd
	stdin  io.Writer
	stdout io.Reader
	events chan any
	done   chan struct{} // closed once the process has exited and its state is settled

	phase       phase
	claudeState string
	startReply  chan error // answered once when initialization ends
	prompt      *submission
	config      *configuration
}

// Events from clients. Each carries a reply channel that the run goroutine
// always answers.
type (
	sendRequest struct {
		clientID, text string
		notify         func(Update)
		reply          chan sendReply
	}
	sendReply struct {
		id  string
		err error
	}
	approveRequest struct {
		clientID, requestID string
		response            json.RawMessage
		reply               chan error
	}
	configureRequest struct {
		config Configuration
		reply  chan error
	}
)

// Events from Claude and timers. Claude's output lines arrive as claudeOutput.
type (
	processExited  struct{ err error }
	controlTimeout struct{ requestID string }
)

func (p *process) run() {
	defer close(p.done)
	go p.read()
	for p.phase != exited {
		p.handle(<-p.events)
	}
}

func (p *process) handle(event any) {
	switch e := event.(type) {
	case claudeOutput:
		p.handleOutput(e)
	case sendRequest:
		id, err := p.submit(e.clientID, e.text, e.notify)
		e.reply <- sendReply{id, err}
	case approveRequest:
		e.reply <- p.answer(e.clientID, e.requestID, e.response)
	case configureRequest:
		p.reconfigure(e.config, e.reply)
	case controlTimeout:
		p.controlTimedOut(e.requestID)
	case processExited:
		p.exit(e.err)
	}
}

// idle reports whether the process can take a prompt or configuration.
func (p *process) idle() bool {
	return p.phase == available && p.claudeState == "idle"
}

func (p *process) exit(err error) {
	reason := "Claude exited"
	if err != nil {
		reason += ": " + err.Error()
	}
	switch p.phase {
	case starting:
		if p.startReply != nil {
			p.startReply <- errors.New(reason)
		}
	case queued, prompting:
		p.endPrompt(Update{Type: "exited", Error: reason})
	case configuring:
		p.endConfiguration(p.config.fail(ErrNotFound))
	}
	p.phase = exited
}

// post delivers an event to the run goroutine. It reports false once the
// process has exited.
func (p *process) post(event any) bool {
	select {
	case p.events <- event:
		return true
	case <-p.done:
		return false
	}
}

func (p *process) send(clientID, text string, notify func(Update)) (string, error) {
	reply := make(chan sendReply, 1)
	if !p.post(sendRequest{clientID, text, notify, reply}) {
		return "", ErrNotFound
	}
	r := <-reply
	return r.id, r.err
}

func (p *process) approve(clientID, requestID string, response json.RawMessage) error {
	reply := make(chan error, 1)
	if !p.post(approveRequest{clientID, requestID, response, reply}) {
		return ErrNotFound
	}
	return <-reply
}

func (p *process) configure(config Configuration) error {
	reply := make(chan error, 1)
	if !p.post(configureRequest{config, reply}) {
		return ErrNotFound
	}
	return <-reply
}
