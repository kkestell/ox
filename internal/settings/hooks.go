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

	"ox/internal/textfile"
)

// defaultHookTimeout matches Claude Code's, so one hook configuration behaves
// the same in both.
const defaultHookTimeout = 60 * time.Second

// Hooks are the commands Ox runs at points in a turn. They follow Claude
// Code's hook schema, so one configuration works for both.
type Hooks struct {
	// PostToolUse runs after each tool call, whatever its outcome.
	PostToolUse []Hook
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

// LoadHooks reads `hooks` from the global settings file at path. A missing
// file or key configures no hooks.
func LoadHooks(path string) (Hooks, error) {
	text, err := textfile.Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Hooks{}, nil
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
		return Hooks{}, nil
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil || events == nil {
		return Hooks{}, invalid(path, "hooks must be an object")
	}
	var hooks Hooks
	for _, event := range slices.Sorted(maps.Keys(events)) {
		if event != "PostToolUse" {
			return Hooks{}, invalid(path, "hook event %s is not supported; the only supported event is PostToolUse", event)
		}
		parsed, err := parseHookGroups(events[event])
		if err != nil {
			return Hooks{}, invalid(path, "hooks: %s: %v", event, err)
		}
		hooks.PostToolUse = parsed
	}
	return hooks, nil
}

// parseHookGroups parses one event's list of matcher groups into hooks, in
// order.
func parseHookGroups(raw json.RawMessage) ([]Hook, error) {
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
