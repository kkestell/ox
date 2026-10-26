package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// defaultHookTimeout matches Claude Code's, so one hook configuration behaves
// the same in both.
const defaultHookTimeout = 60 * time.Second

// Hooks are the commands Ox runs at points in a turn. They follow Claude
// Code's hook schema, so one configuration works for both.
type Hooks struct {
	// SessionStart runs before the first turn of a session in each server
	// process.
	SessionStart []Hook
	// UserPromptSubmit runs before each turn.
	UserPromptSubmit []Hook
	// PostToolUse runs after each tool call, whatever its outcome.
	PostToolUse []Hook
	// Stop runs when the model finishes a turn.
	Stop []Hook
}

// Hook is one command hook and the tool names it applies to.
type Hook struct {
	// Matcher matches the whole tool name. Nil matches every tool.
	Matcher *regexp.Regexp
	Command string
	Timeout time.Duration
}

// Matches reports whether the hook applies to the named tool.
func (h Hook) Matches(tool string) bool {
	return h.Matcher == nil || h.Matcher.MatchString(tool)
}

// LoadHooks reads `hooks` from the global settings file at path, then from
// the launch settings, whose hooks run after the file's. A missing file, unset
// variable, or absent key configures no hooks.
func LoadHooks(path string) (Hooks, error) {
	var hooks Hooks
	for _, path := range []string{path, LaunchVariable} {
		text, err := readSource(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Hooks{}, fmt.Errorf("%s: %w", path, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
			return Hooks{}, invalid(path, "settings must be a JSON object")
		}
		raw, present := fields["hooks"]
		if !present {
			continue
		}
		var events map[string]json.RawMessage
		if err := json.Unmarshal(raw, &events); err != nil || events == nil {
			return Hooks{}, invalid(path, "hooks must be an object")
		}
		for _, event := range slices.Sorted(maps.Keys(events)) {
			target := hooks.event(event)
			if target == nil {
				return Hooks{}, invalid(path, "hook event %s is not supported; the supported events are %s", event, strings.Join(hookEvents, ", "))
			}
			parsed, err := parseHookGroups(events[event], event == "PostToolUse")
			if err != nil {
				return Hooks{}, invalid(path, "hooks: %s: %v", event, err)
			}
			*target = append(*target, parsed...)
		}
	}
	return hooks, nil
}

// hookEvents are the events Ox runs hooks for.
var hookEvents = []string{"PostToolUse", "SessionStart", "Stop", "UserPromptSubmit"}

// event returns the hooks of the named event, or nil for an unsupported one.
func (h *Hooks) event(name string) *[]Hook {
	switch name {
	case "SessionStart":
		return &h.SessionStart
	case "UserPromptSubmit":
		return &h.UserPromptSubmit
	case "PostToolUse":
		return &h.PostToolUse
	case "Stop":
		return &h.Stop
	}
	return nil
}

// parseHookGroups parses one event's list of matcher groups into hooks, in
// order. Only tool events accept a matcher that selects tools.
func parseHookGroups(raw json.RawMessage, tools bool) ([]Hook, error) {
	var groups []json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
		return nil, errors.New("must be a list of matcher groups")
	}
	var hooks []Hook
	for _, raw := range groups {
		var group struct {
			Matcher *string           `json:"matcher"`
			Hooks   []json.RawMessage `json:"hooks"`
		}
		if err := strictDecode(raw, &group); err != nil {
			return nil, fmt.Errorf("matcher group: %v", err)
		}
		if len(group.Hooks) == 0 {
			return nil, errors.New("matcher group: hooks must be a nonempty list")
		}
		var matcher *regexp.Regexp
		if group.Matcher != nil && *group.Matcher != "" && *group.Matcher != "*" {
			if !tools {
				return nil, errors.New("matcher group: only PostToolUse accepts a matcher")
			}
			var err error
			matcher, err = regexp.Compile("^(?:" + *group.Matcher + ")$")
			if err != nil {
				return nil, fmt.Errorf("matcher %q is not a valid regular expression: %v", *group.Matcher, err)
			}
		}
		for _, raw := range group.Hooks {
			var hook struct {
				Type    *string  `json:"type"`
				Command *string  `json:"command"`
				Timeout *float64 `json:"timeout"`
			}
			if err := strictDecode(raw, &hook); err != nil {
				return nil, fmt.Errorf("hook: %v", err)
			}
			switch {
			case hook.Type == nil:
				return nil, errors.New("hook: type is required")
			case *hook.Type != "command":
				return nil, fmt.Errorf("hook type %q is not supported; the only supported type is command", *hook.Type)
			case hook.Command == nil || strings.TrimSpace(*hook.Command) == "" || strings.ContainsRune(*hook.Command, 0):
				return nil, errors.New("hook: command must be nonempty and contain no NUL")
			case hook.Timeout != nil && !(*hook.Timeout > 0 && *hook.Timeout <= 24*60*60):
				return nil, errors.New("hook: timeout must be a number of seconds greater than 0 and at most 86400")
			}
			timeout := defaultHookTimeout
			if hook.Timeout != nil {
				timeout = time.Duration(*hook.Timeout * float64(time.Second))
			}
			hooks = append(hooks, Hook{Matcher: matcher, Command: *hook.Command, Timeout: timeout})
		}
	}
	return hooks, nil
}

// strictDecode decodes a JSON object, rejecting unknown fields.
func strictDecode(raw json.RawMessage, value any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
