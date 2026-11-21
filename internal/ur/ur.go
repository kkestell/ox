// Package ur is Ox's built-in harness, which chats through OpenRouter's chat
// completions API. It runs no process: each turn is one streamed request
// carrying the conversation so far.
package ur

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Endpoint is OpenRouter's chat completions API.
const Endpoint = "https://openrouter.ai/api/v1/chat/completions"

// efforts are the reasoning efforts OpenRouter takes.
var efforts = []string{"max", "xhigh", "high", "medium", "low", "minimal", "none"}

// checkEffort reports whether OpenRouter takes effort; an empty effort sends
// none.
func checkEffort(effort string) error {
	if effort != "" && !slices.Contains(efforts, effort) {
		return fmt.Errorf("Ur has no effort level %q", effort)
	}
	return nil
}

// checkPermissionMode reports whether Ur takes mode. Ur has no tools, so
// manual, where every tool call asks first, is the one mode it honors.
func checkPermissionMode(mode string) error {
	if mode != "manual" {
		return fmt.Errorf("Ur has no permission mode %q", mode)
	}
	return nil
}

// Harness starts agents that post to endpoint. providers maps a model ID to
// the provider order its requests pin; a model without one routes freely.
func Harness(endpoint string, providers map[string][]string) agent.Harness {
	return agent.Harness{
		Start: func(options agent.Options) (agent.Agent, error) {
			key := os.Getenv("OPENROUTER_API_KEY")
			if key == "" {
				return nil, errors.New("OPENROUTER_API_KEY is not set")
			}
			a := &Agent{
				endpoint: endpoint, key: key, providers: providers,
				events: make(chan agent.Event), done: make(chan struct{}),
				onUpdate: options.OnUpdate, onConfigured: options.OnConfigured, settings: options.Settings,
			}
			a.tracker = agent.NewTracker(a.publish)
			return a, nil
		},
		CheckEffort:         checkEffort,
		CheckPermissionMode: checkPermissionMode,
		TakesProviders:      true,
	}
}

// Agent holds one conversation in memory. Every field is owned by the session
// goroutine, except those set at start, which the request goroutine reads.
type Agent struct {
	endpoint     string
	key          string
	providers    map[string][]string
	events       chan agent.Event
	done         chan struct{} // closed on exit, so a request delivers nothing after it
	onUpdate     func(agent.Update)
	onConfigured func(agent.Settings)

	settings agent.Settings
	history  []message
	tracker  *agent.Tracker // inputs not taken and the reported state
	turn     *turn          // the running turn; nil while idle
	exited   bool
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// turn is one request and the reply streamed so far.
type turn struct {
	prompt string // the ID of the input the turn answers
	cancel context.CancelFunc
	reply  strings.Builder
}

// delta is reply text streamed for a turn.
type delta struct {
	turn *turn
	text string
}

// ended ends a turn's request, with the error that ended it early.
type ended struct {
	turn *turn
	err  error
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if update.MessageID == "" && a.turn != nil {
		update.MessageID = a.turn.prompt
	}
	a.onUpdate(update)
}

// deliver passes event to the session goroutine unless the agent has exited.
func (a *Agent) deliver(event agent.Event) {
	select {
	case a.events <- event:
	case <-a.done:
	}
}

// Handle takes a request's output. Output for a turn that is no longer running
// belongs to an interrupted one and is ignored.
func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case delta:
		if e.turn != a.turn {
			return
		}
		e.turn.reply.WriteString(e.text)
		a.publish(agent.Update{Type: agent.UpdateTextDelta, Text: e.text})
	case ended:
		if e.turn != a.turn {
			return
		}
		if e.err != nil {
			a.finish(agent.Result{Text: e.err.Error(), IsError: true})
		} else {
			a.finish(agent.Result{Text: e.turn.reply.String()})
		}
	}
}

func (a *Agent) Snapshot() agent.Snapshot {
	return agent.Snapshot{State: a.tracker.State(), Approvals: []agent.Approval{}, Queued: a.tracker.Queued()}
}

// Send queues the input, and starts a turn with it when no turn is running.
func (a *Agent) Send(input agent.Input) error {
	a.tracker.Accept(input)
	if a.turn == nil {
		a.begin()
	}
	return nil
}

// begin starts a turn with the first queued input. An input sent during a turn
// waits for it to end: a turn is one request, with no step at which to take it.
func (a *Agent) begin() {
	input := a.tracker.Take()
	ctx, cancel := context.WithCancel(context.Background())
	t := &turn{prompt: input.MessageID, cancel: cancel}
	a.turn = t
	a.history = append(a.history, message{Role: "user", Content: input.Content})
	go a.stream(ctx, t, a.request())
}

// request is the body of the next request: the conversation so far, with the
// current settings.
func (a *Agent) request() []byte {
	body := request{Model: a.settings.Model, Messages: a.history, Stream: true}
	if a.settings.Effort != "" {
		body.Reasoning = &reasoning{Effort: a.settings.Effort}
	}
	if order := a.providers[a.settings.Model]; len(order) > 0 {
		body.Provider = &provider{Order: order}
	}
	data, _ := json.Marshal(body) // strings and slices of them always marshal
	return data
}

// finish ends the running turn with result, keeping the reply as the user saw
// it, and begins the next queued input.
func (a *Agent) finish(result agent.Result) {
	if reply := a.turn.reply.String(); reply != "" {
		a.history = append(a.history, message{Role: "assistant", Content: reply})
	}
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	a.tracker.Report(agent.StateIdle, "")
	a.turn.cancel()
	a.turn = nil
	if a.tracker.Len() > 0 {
		a.begin()
	}
}

// Approve has nothing to answer: Ur calls no tools.
func (a *Agent) Approve(string, json.RawMessage) error {
	return agent.ErrApprovalNotFound
}

func (a *Agent) Interrupt() error {
	a.tracker.DropFrom(0)
	if a.turn != nil {
		a.finish(agent.Result{Text: "Interrupted", IsError: true})
	}
	return nil
}

// Configure applies the patch at once; the next request reads the settings.
func (a *Agent) Configure(patch agent.SettingsPatch, done func(error)) {
	if !a.tracker.Idle() {
		done(agent.ErrBusy)
		return
	}
	if settings := patch.Apply(a.settings); settings != a.settings {
		a.settings = settings
		a.onConfigured(settings)
	}
	done(nil)
}

// Parkable is false: the conversation lives only in this agent.
func (a *Agent) Parkable() bool {
	return false
}

func (a *Agent) Stop() {
	a.tracker.DropFrom(0)
	if a.turn != nil {
		a.turn.cancel()
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.turn = nil
	}
	a.exited = true
	close(a.done)
}

func (a *Agent) Exited() bool {
	return a.exited
}
