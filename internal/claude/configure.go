package claude

import (
	"fmt"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type control struct {
	request map[string]any
	apply   func(*agent.Settings)
}

func (c *configuring) fail(err error) error {
	return fmt.Errorf("configure Claude (%s): %w", c.requests[0].request["subtype"], err)
}

func (a *Agent) Configure(patch agent.SettingsPatch, done func(error)) {
	if !a.idle() {
		done(agent.ErrBusy)
		return
	}
	settings := patch.Apply(a.settings)
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
	c := &configuring{requests: requests, done: done}
	a.phase = c
	a.sendControl(c)
}

func (a *Agent) sendControl(c *configuring) {
	c.requestID = newUUID()
	if err := a.input.Control(c.requestID, c.requests[0].request); err != nil {
		a.endConfiguration(c.done, c.fail(err))
		return
	}
	a.after(time.Minute, controlTimeout{c.requestID})
}

func (a *Agent) controlResponse(response controlReply) {
	c, ok := a.phase.(*configuring)
	if !ok || response.RequestID != c.requestID {
		return
	}
	if response.Subtype != "success" {
		err := fmt.Errorf("Claude refused control request (%s): %s", response.Subtype, response.Error)
		a.endConfiguration(c.done, c.fail(err))
		return
	}
	settings := a.settings
	c.requests[0].apply(&settings)
	a.configured(settings)
	c.requests = c.requests[1:]
	if len(c.requests) == 0 {
		a.endConfiguration(c.done, nil)
		return
	}
	a.sendControl(c)
}

// controlTimedOut stops the process when a control request goes unanswered:
// Claude may still apply it, so only a new process, started with the
// requested settings, is known to match them. The process's exit ends the
// configuration.
func (a *Agent) controlTimedOut(requestID string) {
	c, ok := a.phase.(*configuring)
	if !ok || requestID != c.requestID {
		return
	}
	a.proc.Terminate()
	a.phase = timedOut{c.done, c.fail(agent.ErrExited)}
}

// endConfiguration makes the agent ready, which writes any input sent while
// the configuration ran, and reports the outcome.
func (a *Agent) endConfiguration(done func(error), err error) {
	a.ready()
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
