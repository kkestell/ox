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
	requests  []control
	requestID string
	done      func(error)
}

// control is one control request and the setting it changes once Claude
// acknowledges it.
type control struct {
	request map[string]any
	apply   func(*agent.Settings)
}

func (c *configuration) fail(err error) error {
	return fmt.Errorf("configure Claude (%s): %w", c.requests[0].request["subtype"], err)
}

// Configure sends Claude a control request for each setting that differs
// from the process's, one at a time.
func (a *Agent) Configure(settings agent.Settings, done func(error)) {
	if !a.idle() {
		done(agent.ErrBusy)
		return
	}
	var requests []control
	if settings.Model != a.settings.Model {
		requests = append(requests, control{
			map[string]any{"subtype": "set_model", "model": settings.Model},
			func(s *agent.Settings) { s.Model = settings.Model },
		})
	}
	if settings.Effort != a.settings.Effort {
		requests = append(requests, control{
			map[string]any{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": effortLevel(settings.Effort)}},
			func(s *agent.Settings) { s.Effort = settings.Effort },
		})
	}
	if settings.PermissionMode != a.settings.PermissionMode {
		requests = append(requests, control{
			map[string]any{"subtype": "set_permission_mode", "mode": settings.PermissionMode},
			func(s *agent.Settings) { s.PermissionMode = settings.PermissionMode },
		})
	}
	if len(requests) == 0 {
		done(nil)
		return
	}
	a.config = &configuration{requests: requests, done: done}
	a.phase = configuring
	a.sendControl()
}

func (a *Agent) sendControl() {
	c := a.config
	c.requestID = NewUUID()
	if err := a.input.Control(c.requestID, c.requests[0].request); err != nil {
		a.endConfiguration(c.fail(err))
		return
	}
	a.after(time.Minute, controlTimeout{c.requestID})
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
	a.config.requests[0].apply(&a.settings)
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
	done := a.config.done
	a.config = nil
	a.phase = available
	done(err)
}

// effortLevel is the effortLevel flag setting for effort: null clears it for
// a model that takes none.
func effortLevel(effort string) any {
	if effort == "" {
		return nil
	}
	return effort
}
