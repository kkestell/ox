// Package wire is the line protocol between oxd and a ur process: one JSON
// object per line, Commands on ur's stdin and Events on its stdout.
package wire

import "github.com/kkestell/ox/internal/agent"

// Command types, which oxd writes.
const (
	CommandInput     = "input"     // queue Input, and start a turn with it when none runs
	CommandApprove   = "approve"   // answer the pending call named by RequestID
	CommandInterrupt = "interrupt" // drop every untaken input and end the turn
	CommandConfigure = "configure" // use Settings for the next request
)

// Event types, which ur writes.
const (
	EventTaken       = "taken"       // the oldest untaken input, MessageID, entered the running turn
	EventDropped     = "dropped"     // the oldest untaken input, MessageID, was dropped by an interrupt
	EventState       = "state"       // ur's state: running, requires_action, or idle
	EventUpdate      = "update"      // an Update for the transcript, without its time or message ID
	EventResumable   = "resumable"   // the conversation's token, or empty when it could not be resumed
	EventInterrupted = "interrupted" // one interrupt has been carried out
	// A command's process group, Group, has started, or has been killed
	// and reaped. oxd kills each group left running when ur exits.
	EventCommandStarted = "command_started"
	EventCommandEnded   = "command_ended"
)

// Command is a line oxd writes to ur.
type Command struct {
	Type      string          `json:"type"`
	*Input                    // input
	RequestID string          `json:"request_id,omitempty"` // approve
	Behavior  string          `json:"behavior,omitempty"`   // approve: allow or deny
	Message   string          `json:"message,omitempty"`    // approve: a denial's message for the model
	Settings  *agent.Settings `json:"settings,omitempty"`   // configure
}

// Input is a prompt for the model.
type Input struct {
	MessageID string  `json:"message_id"`
	Content   string  `json:"content"`
	Images    []Image `json:"images,omitempty"`
}

// Image is an image attached to an input. Data travels as base64.
type Image struct {
	MimeType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// Event is a line ur writes to oxd.
type Event struct {
	Type      string        `json:"type"`
	MessageID string        `json:"message_id,omitempty"` // taken, dropped
	State     string        `json:"state,omitempty"`      // state
	Update    *agent.Update `json:"update,omitempty"`     // update
	Token     string        `json:"token,omitempty"`      // resumable
	Group     int           `json:"group,omitempty"`      // command_started, command_ended
}
