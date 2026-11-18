// Package fake is a harness that runs no process: it answers each prompt by
// echoing it back, a word at a time. It accepts any settings and asks for no
// approvals.
package fake

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Agent echoes prompts. Its only events are the ticks that stream a reply.
type Agent struct {
	events      chan agent.Event
	done        chan struct{} // closed on exit, so late ticks deliver nothing
	onUpdate    func(agent.Update)
	onResumable func(string)

	state  string   // running or idle
	prompt string   // the client's prompt in progress, by message ID
	reply  string   // the reply in progress
	words  []string // the reply's words still to send
	exited bool
}

type tick struct{}

// Start returns a fake agent. A new conversation reports a random resume
// token at its first result.
func Start(options agent.Options) (agent.Agent, error) {
	a := &Agent{
		events: make(chan agent.Event), done: make(chan struct{}),
		onUpdate: options.OnUpdate, state: "idle",
	}
	if options.Resume == "" {
		a.onResumable = options.OnResumable
	}
	return a, nil
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	update.MessageID = a.prompt
	a.onUpdate(update)
}

// tick delivers the next word after a pause.
func (a *Agent) tick() {
	time.AfterFunc(50*time.Millisecond, func() {
		select {
		case a.events <- tick{}:
		case <-a.done:
		}
	})
}

// Handle sends the reply's next word, or ends the turn once every word is
// sent.
func (a *Agent) Handle(agent.Event) {
	if len(a.words) > 0 {
		a.publish(agent.Update{Type: "text_delta", Text: a.words[0]})
		a.words = a.words[1:]
		a.tick()
		return
	}
	a.publish(agent.Update{Type: "result", Result: &agent.Result{Text: a.reply}})
	if a.onResumable != nil {
		a.onResumable(rand.Text())
		a.onResumable = nil
	}
	a.state = "idle"
	a.publish(agent.Update{Type: "state", State: a.state})
	a.prompt = ""
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.state, Approvals: []agent.Approval{}}
	if a.prompt != "" {
		snapshot.Prompt = &agent.PromptSnapshot{MessageID: a.prompt}
	}
	return snapshot
}

func (a *Agent) Send(text string) (string, error) {
	if a.state != "idle" {
		return "", agent.ErrBusy
	}
	a.prompt = rand.Text()
	a.publish(agent.Update{Type: "prompt", Text: text})
	a.state = "running"
	a.publish(agent.Update{Type: "state", State: a.state})
	a.reply = "You said: " + text
	a.words = strings.SplitAfter(a.reply, " ")
	a.tick()
	return a.prompt, nil
}

func (a *Agent) Approve(string, json.RawMessage) error {
	return agent.ErrApprovalNotFound
}

func (a *Agent) Configure(_ agent.Settings, done func(error)) {
	if a.state != "idle" {
		done(agent.ErrBusy)
		return
	}
	done(nil)
}

func (a *Agent) Parkable() bool {
	return a.state == "idle"
}

// Stop exits at once: there is no process to wait for.
func (a *Agent) Stop() error {
	if a.prompt != "" {
		a.publish(agent.Update{Type: "exited", Error: "stopped"})
		a.prompt = ""
	}
	a.publish(agent.Update{Type: "parked"})
	a.exited = true
	close(a.done)
	return nil
}

func (a *Agent) Exited() bool {
	return a.exited
}
