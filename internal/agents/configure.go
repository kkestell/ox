package agents

import (
	"errors"
	"fmt"
	"time"
)

type Configuration struct {
	Model          *string `json:"model"`
	Effort         *string `json:"effort"`
	PermissionMode *string `json:"permission_mode"`
}

type controlResponse struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

// configuration owns the process in the configuring phase. Its first request
// is in flight under requestID.
type configuration struct {
	requests  []map[string]any
	requestID string
	reply     chan error
}

func (c *configuration) fail(err error) error {
	return fmt.Errorf("configure Claude (%s): %w", c.requests[0]["subtype"], err)
}

func (p *process) reconfigure(config Configuration, reply chan error) {
	if !p.idle() {
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
	p.config = &configuration{requests: requests, reply: reply}
	p.phase = configuring
	p.sendControl()
}

func (p *process) sendControl() {
	c := p.config
	c.requestID = promptID()
	err := p.write(map[string]any{
		"type": "control_request", "request_id": c.requestID, "request": c.requests[0],
	})
	if err != nil {
		p.endConfiguration(c.fail(err))
		return
	}
	requestID := c.requestID
	time.AfterFunc(time.Minute, func() { p.post(controlTimeout{requestID}) })
}

func (p *process) controlResponse(response controlResponse) {
	var err error
	if response.Subtype != "success" {
		err = fmt.Errorf("Claude refused control request (%s): %s", response.Subtype, response.Error)
	}
	switch {
	case p.phase == starting && response.RequestID == "initialize":
		if err == nil {
			p.phase = available
		}
		p.startReply <- err
		p.startReply = nil
	case p.phase == configuring && response.RequestID == p.config.requestID:
		if err != nil {
			p.endConfiguration(p.config.fail(err))
			return
		}
		p.config.requests = p.config.requests[1:]
		if len(p.config.requests) == 0 {
			p.endConfiguration(nil)
			return
		}
		p.sendControl()
	}
}

func (p *process) controlTimedOut(requestID string) {
	if p.phase == configuring && requestID == p.config.requestID {
		p.endConfiguration(p.config.fail(errors.New("no control response within one minute")))
	}
}

func (p *process) endConfiguration(err error) {
	p.config.reply <- err
	p.config = nil
	p.phase = available
}
