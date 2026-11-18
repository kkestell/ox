package agent

import (
	"errors"
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/claude"
)

// Configuration changes settings of a running agent. Nil fields are unchanged;
// an empty Effort restores Claude's default.
type Configuration struct {
	Model          *string
	Effort         *string
	PermissionMode *string
}

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

func (a *Agent) reconfigure(config Configuration, reply chan error) {
	if !a.idle() {
		reply <- ErrBusy
		return
	}
	var requests []map[string]any
	if config.Model != nil {
		requests = append(requests, map[string]any{"subtype": "set_model", "model": *config.Model})
	}
	if config.Effort != nil {
		var effort any
		if *config.Effort != "" {
			effort = *config.Effort
		}
		requests = append(requests, map[string]any{
			"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effort},
		})
	}
	if config.PermissionMode != nil {
		requests = append(requests, map[string]any{"subtype": "set_permission_mode", "mode": *config.PermissionMode})
	}
	if len(requests) == 0 {
		reply <- nil
		return
	}
	a.config = &configuration{requests: requests, reply: reply}
	a.phase = configuring
	a.sendControl()
}

func (a *Agent) sendControl() {
	c := a.config
	c.requestID = newID()
	if err := a.input.Control(c.requestID, c.requests[0]); err != nil {
		a.endConfiguration(c.fail(err))
		return
	}
	requestID := c.requestID
	time.AfterFunc(time.Minute, func() { a.post(controlTimeout{requestID}) })
}

func (a *Agent) controlResponse(response claude.ControlResponse) {
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
