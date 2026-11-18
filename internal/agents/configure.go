package agents

import (
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

// Configuration is exclusive, so only one control request can be pending.
type controlCall struct {
	id     string
	result chan error
}

func (p *process) configure(config Configuration) error {
	p.mu.Lock()
	if p.state == "exited" {
		p.mu.Unlock()
		return ErrNotFound
	}
	if p.state != "idle" || p.prompt != nil || p.configuring {
		p.mu.Unlock()
		return ErrBusy
	}
	p.configuring = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.configuring = false
		p.mu.Unlock()
	}()

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
	for _, request := range requests {
		if err := p.callControl(request); err != nil {
			return fmt.Errorf("configure Claude (%s): %w", request["subtype"], err)
		}
	}
	return nil
}

func (p *process) callControl(request map[string]any) error {
	call := &controlCall{id: promptID(), result: make(chan error, 1)}
	p.mu.Lock()
	if p.state == "exited" {
		p.mu.Unlock()
		return ErrNotFound
	}
	p.control = call
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.control = nil
		p.mu.Unlock()
	}()
	if err := p.write(map[string]any{
		"type": "control_request", "request_id": call.id, "request": request,
	}); err != nil {
		return err
	}
	select {
	case err := <-call.result:
		return err
	case <-p.done:
		return ErrNotFound
	case <-time.After(time.Minute):
		return fmt.Errorf("no control response within one minute")
	}
}

func (p *process) receiveControl(response controlResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	call := p.control
	if call == nil || call.id != response.RequestID {
		return
	}
	var err error
	if response.Subtype != "success" {
		err = fmt.Errorf("Claude refused control request (%s): %s", response.Subtype, response.Error)
	}
	call.result <- err
	p.control = nil
}
