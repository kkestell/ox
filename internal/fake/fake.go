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

	state    string
	settings agent.Settings
	prompt   string // client prompt ID
	reply    string
	words    []string
	config   *configured // the configuration awaiting its tick
	exited   bool
}

// tick streams the next word of prompt's reply.
type tick struct{ prompt string }

// configured completes a configuration a tick after it starts, as Claude
// acknowledges one after a delay.
type configured struct {
	settings agent.Settings
	done     func(agent.Settings, error)
}

func Start(options agent.Options) (agent.Agent, error) {
	a := &Agent{
		events: make(chan agent.Event), done: make(chan struct{}),
		onUpdate: options.OnUpdate, state: agent.StateIdle, settings: options.Settings,
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

// after delivers event through Events a tick from now.
func (a *Agent) after(event agent.Event) {
	time.AfterFunc(50*time.Millisecond, func() {
		select {
		case a.events <- event:
		case <-a.done:
		}
	})
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case *configured:
		a.config = nil
		a.settings = e.settings
		e.done(a.settings, nil)
	case tick:
		if e.prompt != a.prompt {
			return // its prompt was interrupted
		}
		if len(a.words) > 0 {
			a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: a.words[0]})
			a.words = a.words[1:]
			a.after(tick{a.prompt})
			return
		}
		a.finish(agent.Result{Text: a.reply})
	}
}

func (a *Agent) finish(result agent.Result) {
	a.words = nil
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	if a.onResumable != nil {
		a.onResumable(rand.Text())
		a.onResumable = nil
	}
	a.state = agent.StateIdle
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.prompt = ""
}

func (a *Agent) Snapshot() agent.Snapshot {
	snapshot := agent.Snapshot{State: a.state, Approvals: []agent.Approval{}}
	if a.prompt != "" {
		snapshot.Prompt = &agent.PromptSnapshot{MessageID: a.prompt}
	}
	return snapshot
}

func (a *Agent) idle() bool {
	return a.state == agent.StateIdle && a.config == nil
}

func (a *Agent) Send(text string) (string, error) {
	if !a.idle() {
		return "", agent.ErrBusy
	}
	a.prompt = rand.Text()
	a.publish(agent.Update{Type: agent.UpdatePrompt, Text: text})
	a.state = agent.StateRunning
	a.publish(agent.Update{Type: agent.UpdateState, State: a.state})
	a.reply = "You said: " + text
	a.words = strings.SplitAfter(a.reply, " ")
	a.after(tick{a.prompt})
	return a.prompt, nil
}

func (a *Agent) Approve(string, json.RawMessage) error {
	return agent.ErrApprovalNotFound
}

func (a *Agent) Interrupt() error {
	if a.state != agent.StateIdle {
		a.finish(agent.Result{Text: "Interrupted", IsError: true})
	}
	return nil
}

func (a *Agent) Configure(settings agent.Settings, done func(agent.Settings, error)) {
	if !a.idle() {
		done(a.settings, agent.ErrBusy)
		return
	}
	a.config = &configured{settings, done}
	a.after(a.config)
}

func (a *Agent) Parkable() bool {
	return a.idle()
}

func (a *Agent) Stop() error {
	if a.prompt != "" {
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.prompt = ""
	}
	if a.config != nil {
		a.config.done(a.settings, agent.ErrExited)
		a.config = nil
	}
	a.publish(agent.Update{Type: agent.UpdateParked})
	a.exited = true
	close(a.done)
	return nil
}

func (a *Agent) Exited() bool {
	return a.exited
}
