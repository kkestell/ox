// Package ur is oxd's Ur harness. It starts a ur process, the program in
// internal/ur/engine, for each running Ur session and drives it over the
// protocol in internal/ur/wire, and runs ur -ask for one-shot requests.
package ur

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kkestell/ox/internal/agent"
)

// efforts are the reasoning efforts OpenRouter takes.
var efforts = []string{"max", "xhigh", "high", "medium", "low", "minimal", "none"}

// checkEffort reports whether OpenRouter takes effort; an empty effort sends
// none.
func checkEffort(effort string) error {
	if effort != "" && !slices.Contains(efforts, effort) {
		return fmt.Errorf("Ur has no effort level %q", effort)
	}
	return nil
}

// checkPermissionMode reports whether Ur takes mode: manual, where every shell
// call asks first, or auto, where every call runs without asking.
func checkPermissionMode(mode string) error {
	if mode != "manual" && mode != "auto" {
		return fmt.Errorf("Ur has no permission mode %q", mode)
	}
	return nil
}

// Harness starts agents that run command, the ur executable, posting to
// endpoint and keeping their conversations in dir, which is relative to
// oxd's working directory. An empty endpoint leaves
// ur's own, OpenRouter's. providers maps a model ID to the provider order its
// requests pin; a model without one routes freely.
func Harness(command, endpoint, dir string, providers map[string][]string) agent.Harness {
	return agent.Harness{
		Start: func(options agent.Options) (agent.Agent, error) {
			return startAgent(command, endpoint, dir, providers, options)
		},
		CheckEffort:         checkEffort,
		CheckPermissionMode: checkPermissionMode,
		TakesProviders:      true,
		Ask: func(ctx context.Context, model, effort, instructions, text string) (string, error) {
			return ask(ctx, command, endpoint, providers, model, effort, instructions, text)
		},
	}
}

// requestFlags is the part of ur's command line both modes share.
func requestFlags(endpoint string, providers map[string][]string, model, effort string) []string {
	args := []string{"-model=" + model}
	if effort != "" {
		args = append(args, "-effort="+effort)
	}
	if len(providers) > 0 {
		data, _ := json.Marshal(providers) // strings always marshal
		args = append(args, "-providers="+string(data))
	}
	if endpoint != "" {
		args = append(args, "-endpoint="+endpoint)
	}
	return args
}
