package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Scene is a script of sessions, each a series of turns that a scripted
// agent plays in place of a model.
type Scene struct {
	sessions []*sceneSession // top-level sessions and their children, in file order
}

type sceneSession struct {
	title string // a top-level session's title; empty for a child
	text  string // the prompt or message that binds an agent to it
	turns []sceneTurn
}

type sceneUsage struct {
	IntervalSeconds int64   `json:"interval_seconds"`
	UsedPercent     float64 `json:"used_percent"`
	ResetsInMinutes int     `json:"resets_in_minutes"`
}

type sceneApproval struct {
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input"`
}

// sceneFile is the part of a scene file the scripted agent plays.
type sceneFile struct {
	Workspaces []struct {
		Sessions []struct {
			Title    string      `json:"title"`
			Prompt   string      `json:"prompt"`
			Turns    []sceneTurn `json:"turns"`
			Children []struct {
				Name    string      `json:"name"`
				Message string      `json:"message"`
				Turns   []sceneTurn `json:"turns"`
			} `json:"children"`
		} `json:"sessions"`
	} `json:"workspaces"`
}

// script is a scripted agent's place in its scene.
type script struct {
	scene   *Scene
	session *sceneSession // the scene session its first input bound
	next    int           // the index of the session's next turn
}

type sceneTurn struct {
	Updates  []agent.Update `json:"updates"`
	Reply    string         `json:"reply"`
	Usage    []sceneUsage   `json:"usage"`
	End      string         `json:"end"`      // idle, working, or waiting
	Approval *sceneApproval `json:"approval"` // the tool call a waiting turn asks to approve
}

// LoadScene reads and checks the scene file at path.
func LoadScene(path string) (*Scene, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file sceneFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	scene := &Scene{}
	names := map[*sceneSession]string{}
	add := func(name, title, text string, turns []sceneTurn) error {
		session := &sceneSession{title: title, text: text, turns: turns}
		for i, t := range turns {
			if err := checkTurn(t); err != nil {
				return fmt.Errorf("session %q: turn %d: %w", name, i+1, err)
			}
		}
		names[session] = name
		scene.sessions = append(scene.sessions, session)
		return nil
	}
	for _, w := range file.Workspaces {
		for _, s := range w.Sessions {
			if err := add(s.Title, s.Title, s.Prompt, s.Turns); err != nil {
				return nil, err
			}
			for _, c := range s.Children {
				if err := add(c.Name, "", c.Message, c.Turns); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, s := range scene.sessions {
		if strings.TrimSpace(s.text) == "" {
			return nil, fmt.Errorf("session %q: empty prompt or message", names[s])
		}
		for _, other := range scene.sessions {
			if other != s && strings.Contains(s.text, other.text) {
				return nil, fmt.Errorf("session %q: prompt or message contains session %q's", names[s], names[other])
			}
		}
	}
	return scene, nil
}

// checkTurn refuses a turn the scripted agent cannot play.
func checkTurn(t sceneTurn) error {
	switch t.End {
	case agent.StateIdle, "working", "waiting":
	default:
		return fmt.Errorf("end %q is not idle, working, or waiting", t.End)
	}
	if (t.Approval != nil) != (t.End == "waiting") {
		return errors.New("approval belongs on a waiting turn, and only there")
	}
	for _, u := range t.Updates {
		switch u.Type {
		case agent.UpdateReasoningDelta, agent.UpdateTextDelta, agent.UpdateToolUse, agent.UpdateToolResult:
		default:
			return fmt.Errorf("update type %q is not reasoning_delta, text_delta, tool_use, or tool_result", u.Type)
		}
	}
	return nil
}

// Scripted is a harness whose agents play scene's sessions, and whose Ask
// titles them.
func Scripted(scene *Scene) agent.Harness {
	return agent.Harness{
		Start: func(options agent.Options) (agent.Agent, error) {
			a := start(options)
			a.script = &script{scene: scene}
			return a, nil
		},
		Ask:            scene.ask,
		TakesProviders: true,
	}
}

// ask returns the title of the top-level session whose prompt is text.
func (scene *Scene) ask(ctx context.Context, model, effort, instructions, text string) (string, error) {
	for _, s := range scene.sessions {
		if s.title != "" && s.text == text {
			return s.title, nil
		}
	}
	return "", errors.New("the scene has no session with this prompt")
}

// play starts the turn of input: its bound session's next turn. The first
// input binds the session whose prompt or message its Content holds, since a
// message arrives wrapped.
func (a *Agent) play(input agent.Input) {
	sc := a.script
	if sc.session == nil {
		for _, s := range sc.scene.sessions {
			if strings.Contains(input.Content, s.text) {
				sc.session = s
				break
			}
		}
	}
	if sc.session == nil || sc.next == len(sc.session.turns) {
		a.steps = nil
		a.then = func() { a.finish(agent.Result{Text: "the scene has no turn for this input", IsError: true}) }
		a.after(step, tick{a.prompt})
		return
	}
	t := sc.session.turns[sc.next]
	sc.next++
	if len(t.Usage) > 0 {
		now := time.Now()
		var windows []agent.UsageWindow
		for _, u := range t.Usage {
			windows = append(windows, agent.UsageWindow{
				IntervalSeconds: u.IntervalSeconds, UsedPercent: u.UsedPercent,
				ResetsAt: now.Add(time.Duration(u.ResetsInMinutes) * time.Minute),
			})
		}
		a.publish(agent.UsageUpdate(windows))
	}
	a.steps = slices.Concat(t.Updates, deltas(agent.UpdateTextDelta, t.Reply))
	finish := func() { a.finish(agent.Result{Text: t.Reply}) }
	switch t.End {
	case agent.StateIdle:
		a.then = finish
	case "working":
		a.then = nil
	case "waiting":
		a.then = func() {
			a.then = finish
			a.ask(t.Approval.ToolName, t.Approval.Input)
		}
	}
	a.after(step, tick{a.prompt})
}
