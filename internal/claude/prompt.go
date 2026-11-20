package claude

import (
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// submission holds the agent in the prompting phase, from the first client
// prompt written until Claude reports idle with none queued.
type submission struct {
	queued *agent.Prompt // written; Claude has not taken it yet
	last   string        // the latest client prompt Claude took or dropped, which the releasing update names
}

// Send writes an input, which Claude takes into the running turn after its
// current tool call, or runs next if the turn ends first.
func (a *Agent) Send(input agent.Input) (string, error) {
	if a.phase == configuring || a.phase == exited || a.queued() != nil {
		return "", agent.ErrBusy
	}
	id := newUUID()
	if err := a.input.Prompt(id, input.Content); err != nil {
		return "", fmt.Errorf("send prompt to Claude: %w", err)
	}
	if a.phase == available {
		a.prompt = &submission{}
		a.phase = prompting
	}
	a.prompt.queued = &agent.Prompt{MessageID: id, Text: input.Text, From: input.From}
	a.publish(agent.Update{MessageID: id, Type: agent.UpdatePromptQueued, Text: input.Text, From: input.From})
	if a.state == agent.StateIdle {
		a.state = agent.StateRunning
		a.publish(agent.Update{MessageID: id, Type: agent.UpdateState, State: a.state})
	}
	return id, nil
}

// Interrupt asks Claude to stop the running turn and to drop a prompt it has
// not taken yet. Claude ends the turn with an error result and an idle state,
// which end the prompts as usual. Its control response carries nothing Ox
// uses.
func (a *Agent) Interrupt() error {
	if a.state == agent.StateIdle {
		return nil
	}
	request := map[string]any{"subtype": "interrupt"}
	if a.queued() != nil {
		request["cancel_queued"] = true
	}
	if err := a.input.Control(newUUID(), request); err != nil {
		return fmt.Errorf("interrupt Claude: %w", err)
	}
	return nil
}

// queued returns the client prompt Claude has not taken yet, or nil.
func (a *Agent) queued() *agent.Prompt {
	if a.phase != prompting {
		return nil
	}
	return a.prompt.queued
}

// isQueued reports that id is the client prompt Claude has not taken yet.
func (a *Agent) isQueued(id string) bool {
	queued := a.queued()
	return queued != nil && queued.MessageID == id
}

func (a *Agent) commandStarted(id string) {
	if !a.isQueued(id) {
		return
	}
	taken := a.queued()
	a.prompt.queued = nil
	a.prompt.last = id
	a.turn = id
	a.publish(agent.Update{MessageID: id, Type: agent.UpdatePrompt, Text: taken.Text, From: taken.From})
}

// commandCancelled handles Claude dropping a prompt. Claude also reports a
// taken prompt whose turn it interrupted as cancelled, which changes nothing.
func (a *Agent) commandCancelled(id string) {
	if a.isQueued(id) {
		a.dropQueued()
	}
}

// dropQueued ends the queued prompt before Claude took it.
func (a *Agent) dropQueued() {
	dropped := a.queued()
	a.prompt.queued = nil
	a.prompt.last = dropped.MessageID
	a.publish(agent.Update{MessageID: dropped.MessageID, Type: agent.UpdatePromptCancelled, Text: dropped.Text, From: dropped.From})
}

func (a *Agent) sessionState(state string) {
	if state != agent.StateIdle {
		a.setState(state)
		return
	}
	a.withdrawApprovals()
	switch {
	case a.queued() != nil:
		// Claude can report idle before it takes a prompt; keep waiting.
	case a.phase == prompting:
		a.state = state
		a.endPrompt(agent.Update{Type: agent.UpdateState, State: state})
	default:
		a.setState(state)
	}
}

// setState publishes a change to the state clients see.
func (a *Agent) setState(state string) {
	if state != a.state {
		a.state = state
		a.publish(agent.Update{Type: agent.UpdateState, State: state})
	}
}

// endPrompt releases the agent with the final update of the prompts that
// held it, which names the latest of them.
func (a *Agent) endPrompt(final agent.Update) {
	a.withdrawApprovals()
	final.MessageID = a.prompt.last
	a.publish(final)
	a.prompt = nil
	a.turn = ""
	a.phase = available
}

// publish reports an update, tagged with the client prompt whose turn
// produced it unless it names its prompt itself.
func (a *Agent) publish(update agent.Update) {
	update.Time = time.Now()
	if update.MessageID == "" {
		update.MessageID = a.turn
	}
	a.onUpdate(update)
}
