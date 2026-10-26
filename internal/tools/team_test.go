package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"ox/internal/catalog"
	"ox/internal/settings"
	"ox/internal/transcript"
)

type fakeTeam struct {
	worker   bool
	profiles []settings.AgentProfile
	calls    []string
	args     CreateAgentArgs
	to       string
	text     string
	err      error
}

func (f *fakeTeam) IsWorker() bool                    { return f.worker }
func (f *fakeTeam) Profiles() []settings.AgentProfile { return f.profiles }
func (f *fakeTeam) result(name string) (string, error) {
	f.calls = append(f.calls, name)
	return "result of " + name, f.err
}
func (f *fakeTeam) CreateAgent(_ context.Context, args CreateAgentArgs) (string, error) {
	f.args = args
	return f.result(CreateAgent)
}
func (f *fakeTeam) SendMessage(_ context.Context, to, text string) (string, error) {
	f.to, f.text = to, text
	return f.result(SendMessage)
}
func (f *fakeTeam) ListAgents(context.Context) (string, error) { return f.result(ListAgents) }
func (f *fakeTeam) RemoveAgent(_ context.Context, name string) (string, error) {
	f.to = name
	return f.result(RemoveAgent)
}
func (f *fakeTeam) Wait(context.Context) (string, error) { return f.result(Wait) }

var profiles = []settings.AgentProfile{
	{ID: "fast", Description: "Small tasks.", Model: "model-a", Effort: catalog.EffortHigh},
	{ID: "deep", Description: "Hard tasks.", Model: "model-b", Effort: catalog.EffortDefault},
}

func TestTeamSchemasAndDispatchMatchRole(t *testing.T) {
	for _, test := range []struct {
		name string
		team Team
		want []string
	}{
		{"no team", nil, nil},
		{"worker", &fakeTeam{worker: true}, []string{SendMessage, ListAgents}},
		{"orchestrator", &fakeTeam{}, []string{SendMessage, ListAgents, CreateAgent, RemoveAgent, Wait}},
		{"orchestrator with profiles", &fakeTeam{profiles: profiles}, []string{SendMessage, ListAgents, CreateAgent, RemoveAgent, Wait}},
	} {
		t.Run(test.name, func(t *testing.T) {
			box := toolbox(t)
			box.Team = test.team
			var names []string
			for _, raw := range box.Schemas()[len(Schemas()):] {
				var schema struct {
					Function struct {
						Name        string `json:"name"`
						Description string `json:"description"`
						Parameters  struct {
							Type                 string   `json:"type"`
							AdditionalProperties bool     `json:"additionalProperties"`
							Required             []string `json:"required"`
							Properties           struct {
								Profile *struct {
									Enum        []string `json:"enum"`
									Description string   `json:"description"`
								} `json:"profile"`
							} `json:"properties"`
						} `json:"parameters"`
					} `json:"function"`
				}
				if err := json.Unmarshal(raw, &schema); err != nil {
					t.Fatal(err)
				}
				fn := schema.Function
				if fn.Parameters.Type != "object" || fn.Parameters.AdditionalProperties || fn.Parameters.Required == nil {
					t.Errorf("invalid parameters: %s", raw)
				}
				if fn.Name == CreateAgent {
					for _, instruction := range []string{"independent", "worktree", "Merge", "remove", "dir \".\"", "wait"} {
						if !strings.Contains(fn.Description, instruction) {
							t.Errorf("create_agent description missing %q", instruction)
						}
					}
					required, profile := []string{"name", "message"}, fn.Parameters.Properties.Profile
					if test.team.Profiles() == nil {
						if profile != nil || !strings.Contains(fn.Description, "Workers use your model and effort.") {
							t.Errorf("create_agent without profiles: %s", raw)
						}
					} else {
						required = append(required, "profile")
						if profile == nil || !reflect.DeepEqual(profile.Enum, []string{"fast", "deep"}) ||
							!strings.Contains(profile.Description, "- fast (model-a, effort high): Small tasks.") ||
							!strings.Contains(profile.Description, "- deep (model-b, effort default): Hard tasks.") {
							t.Errorf("profile property: %s", raw)
						}
					}
					if !reflect.DeepEqual(fn.Parameters.Required, required) {
						t.Errorf("required = %v", fn.Parameters.Required)
					}
				}
				names = append(names, fn.Name)
			}
			if !reflect.DeepEqual(names, test.want) {
				t.Fatalf("team schemas = %v, want %v", names, test.want)
			}
			for _, name := range []string{CreateAgent, SendMessage, ListAgents, RemoveAgent, Wait} {
				if test.team == nil || (test.team.IsWorker() && name != SendMessage && name != ListAgents) {
					got := box.call(name, object{"name": "reader", "message": "Read"})
					if !reflect.DeepEqual(got, transcript.Failed("Unknown tool: "+name)) {
						t.Errorf("unavailable %s: %+v", name, got)
					}
				}
			}
		})
	}
}

func TestTeamBadArgumentsDoNotDispatchOrAsk(t *testing.T) {
	team := &fakeTeam{}
	box := toolbox(t)
	box.Team = team
	for _, name := range []string{CreateAgent, SendMessage, ListAgents, RemoveAgent, Wait} {
		for _, args := range []string{"not JSON", "null", "[]", `{} {}`, `{"unknown":true}`} {
			if got := box.call(name, args); got.Status != transcript.ToolFailed {
				t.Errorf("%s %s: %+v", name, args, got)
			}
		}
	}
	for _, test := range []struct {
		name string
		args object
	}{
		{CreateAgent, object{}},
		{CreateAgent, object{"name": "orchestrator", "message": "work"}},
		{CreateAgent, object{"name": "Upper", "message": "work"}},
		{CreateAgent, object{"name": "../escape", "message": "work"}},
		{CreateAgent, object{"name": "worker", "message": " \n "}},
		{CreateAgent, object{"name": "worker", "message": "work", "model": "model-a"}},
		{CreateAgent, object{"name": "worker", "message": "work", "profile": "fast"}},
		{SendMessage, object{"to": "worker"}},
		{SendMessage, object{"text": "hello"}},
		{SendMessage, object{"to": "bad/name", "text": "hello"}},
		{RemoveAgent, object{"name": "orchestrator"}},
		{RemoveAgent, object{}},
	} {
		if got := box.call(test.name, test.args); got.Status != transcript.ToolFailed {
			t.Errorf("%s %v: %+v", test.name, test.args, got)
		}
		if got := box.Permission(call(test.name, test.args)); got.Kind != PermissionNone {
			t.Errorf("invalid call asks for permission: %+v", got)
		}
	}
	team.profiles = profiles
	for _, args := range []object{{"name": "worker", "message": "work"}, {"name": "worker", "message": "work", "profile": "slow"}} {
		if got := box.call(CreateAgent, args); got.Status != transcript.ToolFailed || !strings.Contains(got.Text, "profile must be one of fast, deep") {
			t.Errorf("%v: %+v", args, got)
		}
	}
	if len(team.calls) != 0 {
		t.Fatalf("bad arguments reached team: %v", team.calls)
	}
}

func TestTeamDispatchPermissionsAndTitles(t *testing.T) {
	team := &fakeTeam{}
	box := toolbox(t)
	box.Team = team
	team.profiles = profiles
	args := CreateAgentArgs{Name: "worker-1", Message: "Do the work", Profile: "deep", Dir: "."}
	for _, test := range []struct {
		name  string
		args  any
		title string
	}{
		{CreateAgent, args, "Create agent worker-1"},
		{SendMessage, object{"to": "orchestrator", "text": "Report"}, "Message orchestrator"},
		{ListAgents, object{}, "List agents"},
		{RemoveAgent, object{"name": "worker-1"}, "Remove agent worker-1"},
		{Wait, object{}, "Wait for agents"},
	} {
		call := call(test.name, test.args)
		if got := box.Execute(context.Background(), call); !reflect.DeepEqual(got, transcript.Completed("result of "+test.name)) {
			t.Errorf("%s: %+v", test.name, got)
		}
		permission := box.Permission(call)
		if test.name == CreateAgent {
			if permission.Kind != PermissionAgent || permission.Agent != args || permission.Profile != profiles[1] || team.args != args {
				t.Errorf("create args: permission %+v, dispatched %+v", permission, team.args)
			}
		} else if permission.Kind != PermissionNone {
			t.Errorf("%s asks permission", test.name)
		}
		if got := Title(call); got != test.title {
			t.Errorf("title = %q, want %q", got, test.title)
		}
	}
	if team.text != "Report" || team.to != "worker-1" || !reflect.DeepEqual(team.calls, []string{CreateAgent, SendMessage, ListAgents, RemoveAgent, Wait}) {
		t.Fatalf("dispatched team calls: %+v", team)
	}
	team.err = errors.New("cannot remove unmerged branch")
	if got := box.call(RemoveAgent, object{"name": "worker-1"}); !reflect.DeepEqual(got, transcript.Failed(team.err.Error())) {
		t.Errorf("team error = %+v", got)
	}
	team.err = context.Canceled
	if got := box.call(Wait, object{}); got.Status != transcript.ToolCancelled {
		t.Errorf("cancelled wait = %+v", got)
	}
}
