package claude

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

var Harness = agent.Harness{
	Start:               Start,
	CheckPrompt:         checkPrompt,
	CheckEffort:         checkEffort,
	CheckPermissionMode: checkPermissionMode,
}

func checkPrompt(text string) error {
	for first := range strings.FieldsSeq(text) {
		if first == "/model" || first == "/effort" {
			return errors.New("change the model and effort in the session's settings")
		}
		break
	}
	return nil
}

// Send queues an input behind every input Claude has not taken. Claude takes
// it into the running turn after its current tool call, or runs it next if
// the turn ends first. The agent writes it at once when nothing is untaken
// and Claude can take input; otherwise it writes it once Claude has answered
// initialize, a configuration has ended, or Claude has taken or dropped the
// input ahead of it.
func (a *Agent) Send(input agent.Input) error {
	switch a.phase.(type) {
	case available, *prompting:
		if len(a.untaken) == 0 {
			if err := a.write(input); err != nil {
				return err
			}
		}
	case *starting, *configuring, timedOut:
		// Held until ready, or dropped by the end.
	case exited:
		return agent.ErrExited
	}
	a.untaken = append(a.untaken, input)
	a.publish(input.Update(agent.UpdatePromptQueued))
	if a.state == agent.StateIdle {
		a.state = agent.StateRunning
		a.publish(agent.Update{MessageID: input.MessageID, Type: agent.UpdateState, State: a.state})
	}
	return nil
}

// write writes input to Claude, which holds the agent in prompting until
// Claude reports idle with nothing untaken.
func (a *Agent) write(input agent.Input) error {
	if err := a.input.Prompt(input.MessageID, input.Content); err != nil {
		return fmt.Errorf("send prompt to Claude: %w", err)
	}
	p, ok := a.phase.(*prompting)
	if !ok {
		p = &prompting{}
		a.phase = p
	}
	p.input = input
	return nil
}

// writeNext writes the first untaken input. A failed write means Claude is
// exiting, and its exit ends the agent, so every untaken input is dropped.
func (a *Agent) writeNext() {
	if len(a.untaken) == 0 {
		return
	}
	if err := a.write(a.untaken[0]); err != nil {
		a.onError(err)
		a.dropUntaken(0)
	}
}

// ready makes the agent available, once Claude has answered initialize, a
// configuration has ended, or a prompt has been released, and writes the
// first untaken input. With none, the agent is idle.
func (a *Agent) ready() {
	a.phase = available{}
	a.writeNext()
	if _, available := a.phase.(available); available {
		a.setState(agent.StateIdle)
	}
}

// dropUntaken drops the untaken inputs from index from on, oldest first.
func (a *Agent) dropUntaken(from int) {
	dropped := a.untaken[from:]
	a.untaken = a.untaken[:from:from]
	for _, input := range dropped {
		a.publish(input.Update(agent.UpdatePromptCancelled))
	}
}

// Interrupt drops every input not yet written to Claude, then asks Claude to
// stop the running turn and to drop the written input it has not taken.
// Claude ends the turn with an error result and an idle state, which end the
// prompts as usual. Its control response carries nothing Ox uses. While
// Claude starts or a configuration runs, Interrupt writes nothing.
func (a *Agent) Interrupt() error {
	written := 0
	if _, prompting := a.phase.(*prompting); prompting && len(a.untaken) > 0 {
		written = 1
	}
	a.dropUntaken(written)
	switch a.phase.(type) {
	case available, *prompting:
		if a.state == agent.StateIdle {
			return nil
		}
	case *starting, *configuring, timedOut, exited:
		return nil
	}
	request := map[string]any{"subtype": "interrupt"}
	if written > 0 {
		request["cancel_queued"] = true
	}
	if err := a.input.Control(newUUID(), request); err != nil {
		return fmt.Errorf("interrupt Claude: %w", err)
	}
	return nil
}

// waiting returns the prompting phase whose written input is id, or nil.
func (a *Agent) waiting(id string) *prompting {
	if p, ok := a.phase.(*prompting); ok && len(a.untaken) > 0 && id != "" && id == a.untaken[0].MessageID {
		return p
	}
	return nil
}

func (a *Agent) commandStarted(id string) {
	p := a.waiting(id)
	if p == nil {
		return
	}
	taken := a.untaken[0]
	a.untaken = a.untaken[1:]
	p.turn = id
	a.publish(taken.Update(agent.UpdatePrompt))
	a.writeNext()
}

// commandCancelled handles Claude dropping a prompt. Claude also reports a
// taken prompt whose turn it interrupted as cancelled, which changes nothing.
func (a *Agent) commandCancelled(id string) {
	if a.waiting(id) == nil {
		return
	}
	dropped := a.untaken[0]
	a.untaken = a.untaken[1:]
	a.publish(dropped.Update(agent.UpdatePromptCancelled))
	a.writeNext()
}

func (a *Agent) sessionState(state string) {
	if state != agent.StateIdle {
		a.setState(state)
		return
	}
	a.withdrawApprovals()
	if _, starting := a.phase.(*starting); starting {
		// The start's end sets the state.
		return
	}
	p, ok := a.phase.(*prompting)
	switch {
	case len(a.untaken) > 0:
		// Claude can report idle before it takes a prompt, and inputs wait
		// through a configuration; they keep the agent running.
	case ok:
		a.endPrompt(p)
	default:
		a.setState(state)
	}
}

// setState sets Claude's state and publishes a change to the state clients
// see.
func (a *Agent) setState(state string) {
	was := a.published()
	a.state = state
	a.publishState(was)
}

// published is the execution state clients see: background while Claude is
// idle with background tasks running, otherwise state.
func (a *Agent) published() string {
	if a.state == agent.StateIdle && a.background > 0 {
		return agent.StateBackground
	}
	return a.state
}

// publishState publishes the state clients see when it differs from was.
func (a *Agent) publishState(was string) {
	if now := a.published(); now != was {
		a.publish(agent.Update{Type: agent.UpdateState, State: now})
	}
}

// endPrompt releases the agent with an idle state, or background, naming the
// latest input written.
func (a *Agent) endPrompt(p *prompting) {
	a.withdrawApprovals()
	a.state = agent.StateIdle
	a.publish(agent.Update{MessageID: p.input.MessageID, Type: agent.UpdateState, State: a.published()})
	a.ready()
}

// publish reports an update, tagged with the client prompt whose turn
// produced it unless it names its prompt itself.
func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if p, ok := a.phase.(*prompting); ok && update.MessageID == "" {
		update.MessageID = p.turn
	}
	a.onUpdate(update)
}
