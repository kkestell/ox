package claude

import (
	"errors"
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// configuration owns the agent in the configuring phase. Its first request is
// in flight under requestID.
type configuration struct {
	requests  []map[string]any
	requestID string
	reply     chan error
}

func (c *configuration) fail(err error) error {
	return fmt.Errorf("configure Claude (%s): %w", c.requests[0]["subtype"], err)
}

func (a *Agent) reconfigure(config agent.Settings, reply chan error) {
	if a.phase == parking {
		reply <- agent.ErrExited
		return
	}
	if !a.idle() {
		reply <- agent.ErrBusy
		return
	}
	requests := []map[string]any{
		{"subtype": "set_model", "model": config.Model},
		{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effortLevel(config.Effort)}},
		{"subtype": "set_permission_mode", "mode": config.PermissionMode},
	}
	a.config = &configuration{requests: requests, reply: reply}
	a.phase = configuring
	a.sendControl()
}

func (a *Agent) sendControl() {
	c := a.config
	c.requestID = NewUUID()
	if err := a.input.Control(c.requestID, c.requests[0]); err != nil {
		a.endConfiguration(c.fail(err))
		return
	}
	requestID := c.requestID
	time.AfterFunc(time.Minute, func() { a.post(controlTimeout{requestID}) })
}

func (a *Agent) controlResponse(response ControlResponse) {
	if a.phase != configuring || response.RequestID != a.config.requestID {
		return
	}
	if response.Subtype != "success" {
		err := fmt.Errorf("Claude refused control request (%s): %s", response.Subtype, response.Error)
		a.endConfiguration(a.config.fail(err))
		return
	}
	a.config.requests = a.config.requests[1:]
	if len(a.config.requests) == 0 {
		a.endConfiguration(nil)
		return
	}
	a.sendControl()
}

func (a *Agent) controlTimedOut(requestID string) {
	if a.phase == configuring && requestID == a.config.requestID {
		a.endConfiguration(a.config.fail(errors.New("no control response within one minute")))
	}
}

func (a *Agent) endConfiguration(err error) {
	a.config.reply <- err
	a.config = nil
	a.phase = available
}

// effortLevel is the effortLevel flag setting for effort: null clears it for
// a model that takes none.
func effortLevel(effort string) any {
	if effort == "" {
		return nil
	}
	return effort
}
