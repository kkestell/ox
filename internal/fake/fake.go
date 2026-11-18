// Package fake is a harness that runs no process: it answers each prompt by
// echoing it back, a word at a time. It accepts any settings, asks for no
// approvals, and never parks.
package fake

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Agent runs on one goroutine, which runs each event in order.
type Agent struct {
	events chan func()
	done   chan struct{}

	// Owned by the run goroutine.
	state       string // running or idle
	prompt      string // the client's prompt in progress, by message ID
	subscribers []agent.Subscriber
	onResumable func(string)
	exited      bool
}

// Start returns a fake agent. A new conversation reports a random resume
// token at its first result.
func Start(options agent.Options) (agent.Agent, error) {
	a := &Agent{
		events: make(chan func()), done: make(chan struct{}),
		state: "idle", subscribers: options.Subscribers,
	}
	if options.Resume == "" {
		a.onResumable = options.OnResumable
	}
	go a.run()
	return a, nil
}

func (a *Agent) run() {
	for !a.exited {
		(<-a.events)()
	}
	close(a.done)
}

// call runs f on the agent's goroutine and waits for it, or returns
// ErrExited once the agent has exited.
func (a *Agent) call(f func()) error {
	ran := make(chan struct{})
	select {
	case a.events <- func() { f(); close(ran) }:
		<-ran
		return nil
	case <-a.done:
		return agent.ErrExited
	}
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	update.MessageID = a.prompt
	for _, s := range a.subscribers {
		s.Publish(update)
	}
}

func (a *Agent) Inspect(inspect func(agent.Snapshot)) error {
	return a.call(func() {
		snapshot := agent.Snapshot{State: a.state, Approvals: []agent.Approval{}}
		if a.prompt != "" {
			snapshot.Prompt = &agent.PromptSnapshot{MessageID: a.prompt}
		}
		inspect(snapshot)
	})
}

func (a *Agent) Send(text string) (string, error) {
	var id string
	busy := false
	err := a.call(func() {
		if a.state != "idle" {
			busy = true
			return
		}
		id = rand.Text()
		a.prompt = id
		a.publish(agent.Update{Type: "prompt", Text: text})
		a.state = "running"
		a.publish(agent.Update{Type: "state", State: a.state})
		go a.reply(text)
	})
	if busy {
		return "", agent.ErrBusy
	}
	return id, err
}

// reply streams the echo, then ends the turn.
func (a *Agent) reply(text string) {
	reply := "You said: " + text
	for _, word := range strings.SplitAfter(reply, " ") {
		time.Sleep(50 * time.Millisecond)
		if a.call(func() { a.publish(agent.Update{Type: "text_delta", Text: word}) }) != nil {
			return
		}
	}
	a.call(func() {
		a.publish(agent.Update{Type: "result", Result: &agent.Result{Text: reply}})
		if a.onResumable != nil {
			a.onResumable(rand.Text())
			a.onResumable = nil
		}
		a.state = "idle"
		a.publish(agent.Update{Type: "state", State: a.state})
		a.prompt = ""
	})
}

func (a *Agent) Approve(string, json.RawMessage) error {
	return agent.ErrApprovalNotFound
}

func (a *Agent) Configure(agent.Settings) error {
	busy := false
	err := a.call(func() { busy = a.state != "idle" })
	if busy {
		return agent.ErrBusy
	}
	return err
}

func (a *Agent) Stop() error {
	a.call(func() {
		if a.prompt != "" {
			a.publish(agent.Update{Type: "exited", Error: "stopped"})
			a.prompt = ""
		}
		a.publish(agent.Update{Type: "parked"})
		a.exited = true
	})
	return nil
}

func (a *Agent) Done() <-chan struct{} {
	return a.done
}
