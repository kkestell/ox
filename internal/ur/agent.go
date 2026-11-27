package ur

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/process"
	"github.com/kkestell/ox/internal/ur/wire"
)

// killAfter is how long ur has to exit once asked before it is killed.
const killAfter = 10 * time.Second

// lostConversation tells the user that the next prompt starts a new
// conversation. The prompt that found the conversation gone is dropped, so
// the user decides whether to send it without the earlier context.
const lostConversation = "Ur could not resume the earlier conversation; the next prompt starts a new one without its context"

// Agent drives one ur process. ur decides when an input is taken or dropped
// and reports its state; the agent keeps the inputs ur has not reported, the
// pending approvals, and the interrupts waiting on ur, and is the one place
// that tags and times ur's updates. The session goroutine makes every call.
type Agent struct {
	proc   *process.Process
	stdin  *json.Encoder
	events chan agent.Event
	done   chan struct{} // closed when the agent ends, so ur's later output is discarded

	tracker      *agent.Tracker   // inputs written and not yet reported taken or dropped
	approvals    []agent.Approval // pending, in arrival order
	turn         string           // the ID of the latest input ur took into its running turn
	stops        []func(error)    // interrupts written and not yet carried out, oldest first
	settings     agent.Settings
	exited       bool
	onUpdate     func(agent.Update)
	onResumable  func(string)
	onConfigured func(agent.Settings)
}

// startAgent launches ur in the session's workspace.
func startAgent(command, endpoint, dir string, providers map[string][]string, options agent.Options) (agent.Agent, error) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		return nil, errors.New("OPENROUTER_API_KEY is not set")
	}
	// ur runs in the session's workspace, so a relative dir is made oxd's.
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-dir=" + dir, "-permission-mode=" + options.Settings.PermissionMode},
		requestFlags(endpoint, providers, options.Settings.Model, options.Settings.Effort)...)
	if options.Resume != "" {
		args = append(args, "-resume="+options.Resume)
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = options.Workspace
	a := &Agent{
		events: make(chan agent.Event), done: make(chan struct{}), settings: options.Settings,
		onUpdate: options.OnUpdate, onResumable: options.OnResumable, onConfigured: options.OnConfigured,
	}
	// On SIGTERM ur kills its commands' process groups before it exits.
	c := &commands{}
	proc, stdin, err := process.Start(cmd, "ur", options.SessionID, syscall.SIGTERM, c.parse, c.kill, a.events, a.done)
	if err != nil {
		return nil, err
	}
	a.proc, a.stdin = proc, json.NewEncoder(stdin)
	a.tracker = agent.NewTracker(a.publish)
	return a, nil
}

// protocolError is a line of ur's stdout that is not an event.
type protocolError string

// commands holds the process groups of the commands ur reported started and
// not ended. Only ur's reader goroutine uses it, through parse and kill, so it
// sees every command event in order and ur's exit, whether or not the agent
// has ended.
type commands struct {
	groups []int
}

// parse reads one line of ur's stdout. It keeps the command events itself and
// returns nil for a valid one, so they never reach the agent.
func (c *commands) parse(line []byte) agent.Event {
	var e wire.Event
	if err := json.Unmarshal(line, &e); err != nil {
		return protocolError(fmt.Sprintf("%v: %q", err, line))
	}
	switch e.Type {
	case wire.EventCommandStarted:
		// The group reaches kill, where 0 or a negative number names other
		// processes than a command's.
		if e.Group <= 0 {
			return protocolError(fmt.Sprintf("command_started for group %d", e.Group))
		}
		c.groups = append(c.groups, e.Group)
		return nil
	case wire.EventCommandEnded:
		i := slices.Index(c.groups, e.Group)
		if i < 0 {
			return protocolError(fmt.Sprintf("command_ended for group %d, which did not start", e.Group))
		}
		c.groups = slices.Delete(c.groups, i, i+1)
		return nil
	}
	return e
}

// kill runs on the reader goroutine once ur has exited and its output is read.
// Only ur ends its commands, so the commands it left end here. After SIGTERM
// ur kills its commands without reporting them ended, so a group may already
// be gone.
func (c *commands) kill() {
	for _, group := range c.groups {
		syscall.Kill(-group, syscall.SIGKILL)
	}
	c.groups = nil
}

func (a *Agent) Events() <-chan agent.Event {
	return a.events
}

func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if update.MessageID == "" {
		update.MessageID = a.turn
	}
	a.onUpdate(update)
}

func (a *Agent) write(command wire.Command) error {
	return a.stdin.Encode(command)
}

func (a *Agent) Handle(event agent.Event) {
	switch e := event.(type) {
	case wire.Event:
		if err := a.handle(e); err != nil {
			a.broke(err.Error())
		}
	case protocolError:
		a.broke(string(e))
	case process.Exit:
		reason := "Ur exited"
		if e.Err != nil {
			reason += ": " + e.Err.Error()
		}
		if e.Stderr != "" {
			reason += "\n" + e.Stderr
		}
		a.end(reason)
	}
}

// handle takes one of ur's events, or reports how it breaks the protocol.
func (a *Agent) handle(e wire.Event) error {
	switch e.Type {
	case wire.EventTaken:
		if err := a.first(e); err != nil {
			return err
		}
		// The prompt update and the state change Take publishes name it.
		a.turn = e.MessageID
		a.tracker.Take()
	case wire.EventDropped:
		if err := a.first(e); err != nil {
			return err
		}
		a.tracker.DropFirst()
	case wire.EventState:
		switch e.State {
		case agent.StateRunning, agent.StateRequiresAction, agent.StateIdle:
		default:
			return fmt.Errorf("state %q", e.State)
		}
		a.tracker.Report(e.State, "")
		if e.State == agent.StateIdle {
			a.turn = ""
		}
	case wire.EventUpdate:
		if e.Update == nil {
			return errors.New("update event without an update")
		}
		return a.update(*e.Update)
	case wire.EventResumable:
		a.onResumable(e.Token)
		if e.Token == "" {
			a.proc.Terminate(killAfter)
			a.end(lostConversation)
		}
	case wire.EventInterrupted:
		if len(a.stops) == 0 {
			return errors.New("interrupted without an interrupt")
		}
		stop := a.stops[0]
		a.stops = a.stops[1:]
		stop(nil)
	default:
		return fmt.Errorf("event type %q", e.Type)
	}
	return nil
}

// first checks that a taken or dropped event names the oldest input ur has
// not reported.
func (a *Agent) first(e wire.Event) error {
	if first, ok := a.tracker.First(); !ok || first.MessageID != e.MessageID {
		return fmt.Errorf("%s %q is not the oldest untaken input", e.Type, e.MessageID)
	}
	return nil
}

// update publishes one of ur's transcript updates, keeping the approvals it
// asks for. A cancellation of an approval already answered is not published.
func (a *Agent) update(u agent.Update) error {
	switch u.Type {
	case agent.UpdateTextDelta, agent.UpdateReasoningDelta, agent.UpdateToolUse, agent.UpdateToolResult, agent.UpdateResult:
	case agent.UpdateApproval:
		a.approvals = append(a.approvals, agent.Approval{RequestID: u.RequestID, Request: u.Request})
	case agent.UpdateApprovalCancelled:
		i := a.approval(u.RequestID)
		if i < 0 {
			return nil
		}
		a.approvals = slices.Delete(a.approvals, i, i+1)
	default:
		return fmt.Errorf("update type %q", u.Type)
	}
	a.publish(u)
	return nil
}

// approval is the index of the pending approval requestID, or -1.
func (a *Agent) approval(requestID string) int {
	return slices.IndexFunc(a.approvals, func(p agent.Approval) bool { return p.RequestID == requestID })
}

// broke stops ur, which has broken the protocol.
func (a *Agent) broke(detail string) {
	a.proc.Terminate(killAfter)
	a.end("Ur broke its protocol: " + detail)
}

func (a *Agent) Snapshot() agent.Snapshot {
	return agent.Snapshot{State: a.tracker.State(), Approvals: append([]agent.Approval{}, a.approvals...), Queued: a.tracker.Queued()}
}

// Send writes the input to ur, which takes it into its running turn before
// the turn's next request, or starts a turn with it.
func (a *Agent) Send(input agent.Input) error {
	if a.exited {
		return agent.ErrExited
	}
	in := &wire.Input{MessageID: input.MessageID, Content: input.Content}
	for _, image := range input.Images {
		in.Images = append(in.Images, wire.Image{MimeType: image.MimeType, Data: image.Data})
	}
	if err := a.write(wire.Command{Type: wire.CommandInput, Input: in}); err != nil {
		return fmt.Errorf("send input to Ur: %w", err)
	}
	a.tracker.Accept(input)
	return nil
}

// Approve answers the pending shell call's approval. Allow runs the command as
// the model wrote it; deny tells the model the user's message.
func (a *Agent) Approve(requestID string, raw json.RawMessage) error {
	var response struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Behavior != "allow" && response.Behavior != "deny") {
		return errors.New("response must be an allow or deny permission response")
	}
	i := a.approval(requestID)
	if i < 0 {
		return agent.ErrApprovalNotFound
	}
	if err := a.write(wire.Command{Type: wire.CommandApprove, RequestID: requestID, Behavior: response.Behavior, Message: response.Message}); err != nil {
		return err
	}
	a.approvals = slices.Delete(a.approvals, i, i+1)
	a.publish(agent.Update{Type: agent.UpdateApprovalAnswered, RequestID: requestID})
	return nil
}

// Interrupt asks ur to drop its untaken inputs and end its turn, and calls
// done once ur has, after the stopped turn's updates.
func (a *Agent) Interrupt(done func(error)) {
	if a.exited || a.tracker.Idle() {
		done(nil)
		return
	}
	if err := a.write(wire.Command{Type: wire.CommandInterrupt}); err != nil {
		done(err)
		return
	}
	a.stops = append(a.stops, done)
}

// Idle is the tracker's: ur has reported idle and holds no input, and ur
// starts a turn only with an input.
func (a *Agent) Idle() bool {
	return a.tracker.Idle()
}

// Configure sends ur the changed settings, which it uses from the next
// request. ur reads them before any later input.
func (a *Agent) Configure(patch agent.SettingsPatch, done func(error)) {
	if a.exited {
		done(agent.ErrExited)
		return
	}
	if !a.Idle() {
		done(agent.ErrBusy)
		return
	}
	settings := patch.Apply(a.settings)
	if settings != a.settings {
		if err := a.write(wire.Command{Type: wire.CommandConfigure, Settings: &settings}); err != nil {
			done(err)
			return
		}
		a.settings = settings
		a.onConfigured(settings)
	}
	done(nil)
}

// Parkable is true while idle: the file holds the whole conversation, and no
// command outlives its turn.
func (a *Agent) Parkable() bool {
	return a.Idle()
}

// Stop asks ur to exit, which ends its turn and kills its commands, and ends
// the agent at once. ur exits, or is killed, in the background.
func (a *Agent) Stop() {
	a.proc.Terminate(killAfter)
	reason := ""
	if a.tracker.State() != agent.StateIdle {
		reason = "stopped"
	}
	a.end(reason)
}

// end ends the agent, for Stop, ur's exit, a lost conversation, or a broken
// protocol. It drops every input ur has not taken and withdraws approvals. A
// nonempty reason is published as exited, naming the running turn's input, or
// else the latest untaken input.
func (a *Agent) end(reason string) {
	final := agent.Update{Type: agent.UpdateExited, Error: reason, MessageID: a.turn}
	if queued := a.tracker.Queued(); final.MessageID == "" && len(queued) > 0 {
		final.MessageID = queued[len(queued)-1].MessageID
	}
	a.tracker.End()
	for _, approval := range a.approvals {
		a.publish(agent.Update{Type: agent.UpdateApprovalCancelled, RequestID: approval.RequestID})
	}
	a.approvals = nil
	if reason != "" {
		a.publish(final)
	}
	for _, stop := range a.stops {
		stop(nil)
	}
	a.stops = nil
	a.turn = ""
	a.exited = true
	close(a.done)
}

func (a *Agent) Exited() bool {
	return a.exited
}

// Reaped is closed once ur has exited and the process groups of the commands
// it reported started and not ended have been killed.
func (a *Agent) Reaped() <-chan struct{} {
	return a.proc.Reaped()
}
