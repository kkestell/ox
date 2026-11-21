// Package ur is Ox's built-in harness, which chats through OpenRouter's chat
// completions API. It runs no process: each turn is one streamed request
// carrying the conversation so far, which Ur keeps in a file of its own.
package ur

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// errLost refuses a start whose saved conversation cannot be read.
var errLost = errors.New("Ur could not resume the earlier conversation; the next prompt starts a new one without its context")

// Harness starts agents that post to endpoint and keep their conversations in
// dir. providers maps a model ID to the provider order its requests pin; a
// model without one routes freely.
func Harness(endpoint, dir string, providers map[string][]string) agent.Harness {
	return agent.Harness{
		Start: func(options agent.Options) (agent.Agent, error) {
			key := os.Getenv("OPENROUTER_API_KEY")
			if key == "" {
				return nil, errors.New("OPENROUTER_API_KEY is not set")
			}
			a := &Agent{
				endpoint: endpoint, key: key, providers: providers, dir: dir, id: options.Resume,
				events: make(chan agent.Event), done: make(chan struct{}),
				onUpdate: options.OnUpdate, onResumable: options.OnResumable,
				onConfigured: options.OnConfigured, onError: options.OnError, settings: options.Settings,
			}
			if a.id == "" {
				a.id = rand.Text()
			} else if err := a.load(); err != nil {
				options.OnError(fmt.Errorf("resume conversation: %w", err))
				options.OnResumable("")
				return nil, errLost
			}
			a.tracker = agent.NewTracker(a.publish)
			return a, nil
		},
		CheckEffort:         checkEffort,
		CheckPermissionMode: checkPermissionMode,
		TakesProviders:      true,
	}
}

// Agent holds one conversation, which it appends to the file <dir>/<id>.jsonl
// and announces by its ID as the resume token. Every field is owned by the
// session goroutine, except those set at start, which the request goroutine
// reads.
type Agent struct {
	endpoint     string
	key          string
	providers    map[string][]string
	dir          string
	id           string
	events       chan agent.Event
	done         chan struct{} // closed on exit, so a request delivers nothing after it
	onUpdate     func(agent.Update)
	onResumable  func(string)
	onConfigured func(agent.Settings)
	onError      func(error)

	settings agent.Settings
	history  []entry        // exactly the file's entries; only save adds to it
	tracker  *agent.Tracker // inputs not taken and the reported state
	turn     *turn          // the running turn; nil while idle
	exited   bool
}

// entry is one line of the conversation file: a chat message, and on a reply,
// the model that wrote it.
type entry struct {
	message
	Model string `json:"model,omitempty"`
}

func (a *Agent) path() string {
	return filepath.Join(a.dir, a.id+".jsonl")
}

// load reads the conversation file into history.
func (a *Agent) load() error {
	f, err := os.Open(a.path())
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	for {
		var e entry
		err := decoder.Decode(&e)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		a.history = append(a.history, e)
	}
}

// save appends e to the conversation file, and to history once it is
// written. Saving the first entry makes the conversation resumable.
func (a *Agent) save(e entry) error {
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(e) // strings always marshal
	_, err = f.Write(append(data, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	a.history = append(a.history, e)
	if len(a.history) == 1 {
		a.onResumable(a.id)
	}
	return nil
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
	if err := a.save(entry{Role: "user", Content: input.Content}); err != nil {
		a.finish(agent.Result{Text: "Ur could not save the prompt: " + err.Error(), IsError: true})
		return
	}
	go a.stream(ctx, t, a.request())
}

// request is the body of the next request: the conversation so far, with the
// current settings.
func (a *Agent) request() []byte {
	messages := make([]message, len(a.history))
	for i, e := range a.history {
		messages[i] = e.message
	}
	body := request{Model: a.settings.Model, Messages: messages, Stream: true}
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
	if err := a.keepReply(); err != nil {
		text := "Ur could not save the reply: " + err.Error()
		if result.IsError {
			text = result.Text + "\n" + text
		}
		result = agent.Result{Text: text, IsError: true}
	}
	a.publish(agent.Update{Type: agent.UpdateResult, Result: &result})
	a.tracker.Report(agent.StateIdle, "")
	a.turn.cancel()
	a.turn = nil
	if a.tracker.Len() > 0 {
		a.begin()
	}
}

// keepReply saves the running turn's reply as far as it streamed, if any.
func (a *Agent) keepReply() error {
	reply := a.turn.reply.String()
	if reply == "" {
		return nil
	}
	return a.save(entry{Role: "assistant", Content: reply, Model: a.settings.Model})
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

// Parkable is true while idle: the file holds the whole conversation, and Ur
// runs no background tasks.
func (a *Agent) Parkable() bool {
	return a.tracker.Idle()
}

func (a *Agent) Stop() {
	a.tracker.DropFrom(0)
	if a.turn != nil {
		a.turn.cancel()
		if err := a.keepReply(); err != nil {
			a.onError(fmt.Errorf("save the reply: %w", err))
		}
		a.publish(agent.Update{Type: agent.UpdateExited, Error: "stopped"})
		a.turn = nil
	}
	a.exited = true
	close(a.done)
}

func (a *Agent) Exited() bool {
	return a.exited
}
