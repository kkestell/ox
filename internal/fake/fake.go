package fake

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type Agent struct {
	events      chan agent.Event
	done        chan struct{} // closed on exit, so late ticks deliver nothing
	onUpdate    func(agent.Update)
	onResumable func(string)

	state  string
	prompt string // client prompt ID
	reply  string
	words  []string
	exited bool
}

type tick struct{}

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

func (a *Agent) tick() {
	time.AfterFunc(50*time.Millisecond, func() {
		select {
		case a.events <- tick{}:
		case <-a.done:
		}
	})
}

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

// Stop completes synchronously.
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
