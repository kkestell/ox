package claude

import (
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// configuration owns the agent in the configuring phase. Its first request is
// in flight under requestID, which is empty once the request has timed out and
// the process is stopping.
type configuration struct {
	requests  []control
	requestID string
	done      func(agent.Settings, error)
}

type control struct {
	request map[string]any
	apply   func(*agent.Settings)
}

func (c *configuration) fail(err error) error {
	return fmt.Errorf("configure Claude (%s): %w", c.requests[0].request["subtype"], err)
}

func (a *Agent) Configure(settings agent.Settings, done func(agent.Settings, error)) {
	if !a.idle() {
		done(a.settings, agent.ErrBusy)
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
		done(a.settings, nil)
		return
	}
	a.config = &configuration{requests: requests, done: done}
	a.phase = configuring
	a.sendControl()
}

func (a *Agent) sendControl() {
	c := a.config
	c.requestID = newUUID()
	if err := a.input.Control(c.requestID, c.requests[0].request); err != nil {
		a.endConfiguration(c.fail(err))
		return
	}
	a.after(time.Minute, controlTimeout{c.requestID})
}

func (a *Agent) controlResponse(response controlReply) {
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

// controlTimedOut stops the process when a control request goes unanswered:
// Claude may still apply it, so only a new process, started with the
// requested settings, is known to match them. The process's exit ends the
// configuration.
func (a *Agent) controlTimedOut(requestID string) {
	if a.phase != configuring || requestID != a.config.requestID {
		return
	}
	a.config.requestID = ""
	if err := a.Stop(); err != nil {
		a.endConfiguration(a.config.fail(err))
	}
}

func (a *Agent) endConfiguration(err error) {
	done := a.config.done
	a.config = nil
	a.phase = available
	done(a.settings, err)
}

// effortLevel is the effortLevel flag setting for effort: null clears it for
// a model that takes none.
func effortLevel(effort string) any {
	if effort == "" {
		return nil
	}
	return effort
}
