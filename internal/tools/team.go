package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ox/internal/settings"
	"ox/internal/transcript"
)

const (
	CreateAgent = "create_agent"
	SendMessage = "send_message"
	ListAgents  = "list_agents"
	RemoveAgent = "remove_agent"
	Wait        = "wait"
)

// Team is the session's access to its team. The server owns worker lifecycles.
type Team interface {
	IsWorker() bool
	// Profiles are the models and efforts workers may use. Without profiles,
	// workers use the orchestrator's.
	Profiles() []settings.AgentProfile
	CreateAgent(context.Context, CreateAgentArgs) (string, error)
	SendMessage(ctx context.Context, to, text string) (string, error)
	ListAgents(context.Context) (string, error)
	RemoveAgent(ctx context.Context, name string) (string, error)
	Wait(context.Context) (string, error)
}

type CreateAgentArgs struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Profile string `json:"profile,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

func (t *Toolbox) teamSchemas() []json.RawMessage {
	if t.Team == nil {
		return nil
	}
	schemas := []json.RawMessage{
		teamSchema(SendMessage, "Send a message to a team member by name, or to orchestrator. An idle worker starts working on the message.", map[string]any{"to": stringProperty("Recipient name, or orchestrator."), "text": stringProperty("Message to send.")}, []string{"to", "text"}),
		teamSchema(ListAgents, "List the team members with their name, model, effort, workspace, branch, and whether they are working.", map[string]any{}, nil),
	}
	if t.Team.IsWorker() {
		return schemas
	}
	description := "Create a worker and give it work. Run only independent work in parallel. Give a worker a worktree when it will change files: omit dir to create a worktree and branch from your HEAD. Merge its branch from your checkout, then remove the worker. Use dir \".\" for a worker that only reads. Call wait after giving out work. Workers run commands without asking for permission and cannot create workers."
	properties := map[string]any{
		"name":    map[string]any{"type": "string", "pattern": "^[a-z0-9-]+$", "description": "Unique worker name using lowercase letters, digits, and hyphens; not orchestrator."},
		"message": stringProperty("Work for the worker to do."),
		"dir":     optionalString("Omit for a worker that changes files: Ox creates its Git worktree and branch ox/<name> from your HEAD. Set it only to run the worker in an existing directory instead, such as \".\" for a worker that only reads: absolute, ~/ relative to home, or relative to your workspace. Required outside a Git checkout."),
	}
	required := []string{"name", "message"}
	if profiles := t.Team.Profiles(); len(profiles) > 0 {
		var ids, lines []string
		for _, profile := range profiles {
			ids = append(ids, profile.ID)
			lines = append(lines, fmt.Sprintf("- %s (%s, effort %s): %s", profile.ID, profile.Model, profile.Effort, profile.Description))
		}
		properties["profile"] = map[string]any{"type": "string", "enum": ids, "description": "The worker's agent profile, which sets its model and effort:\n" + strings.Join(lines, "\n")}
		required = append(required, "profile")
	} else {
		description += " Workers use your model and effort."
	}
	return append(schemas,
		teamSchema(CreateAgent, description, properties, required),
		teamSchema(RemoveAgent, "Cancel a worker, remove its worktree and branch, and delete its session. Merge its branch from your checkout first. Git refuses removal of uncommitted changes or an unmerged branch. A worker using dir leaves that directory in place.", map[string]any{"name": stringProperty("Worker name to remove.")}, []string{"name"}),
		teamSchema(Wait, "After giving out work, wait for messages or until no worker is working. Return waiting messages. If no messages and no working workers remain, return immediately. Cancellation stops waiting.", map[string]any{}, nil),
	)
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": 1}
}

// optionalString accepts an empty string as omitted, since some models fill in
// every property.
func optionalString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func teamSchema(name, description string, properties map[string]any, required []string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	raw, err := json.Marshal(map[string]any{"type": "function", "function": map[string]any{
		"name": name, "description": description,
		"parameters": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
	}})
	if err != nil {
		panic(err)
	}
	return raw
}

func (t *Toolbox) hasTeamTool(name string) bool {
	if t.Team == nil {
		return false
	}
	switch name {
	case SendMessage, ListAgents:
		return true
	case CreateAgent, RemoveAgent, Wait:
		return !t.Team.IsWorker()
	}
	return false
}

func decodeTeamArguments(arguments string, value any) error {
	if !strings.HasPrefix(strings.TrimSpace(arguments), "{") {
		return errors.New("arguments must be a JSON object")
	}
	return decodeArguments(arguments, value)
}

func validAgentName(name string) bool {
	if name == "" || name == "orchestrator" {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// parseCreateAgent decodes and checks create_agent's arguments and returns the
// selected profile, which is zero without profiles.
func (t *Toolbox) parseCreateAgent(arguments string) (CreateAgentArgs, settings.AgentProfile, error) {
	var args CreateAgentArgs
	if err := decodeTeamArguments(arguments, &args); err != nil {
		return args, settings.AgentProfile{}, err
	}
	profile, err := selectProfile(t.Team.Profiles(), args.Profile)
	if err != nil {
		return args, profile, err
	}
	if !validAgentName(args.Name) {
		return args, profile, errors.New("name must contain only lowercase letters, digits, and hyphens, and must not be orchestrator")
	}
	if strings.TrimSpace(args.Message) == "" {
		return args, profile, errors.New("message is required")
	}
	return args, profile, nil
}

func selectProfile(profiles []settings.AgentProfile, id string) (settings.AgentProfile, error) {
	if len(profiles) == 0 {
		if id != "" {
			return settings.AgentProfile{}, errors.New("no agent profiles are configured; omit profile")
		}
		return settings.AgentProfile{}, nil
	}
	var ids []string
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, nil
		}
		ids = append(ids, profile.ID)
	}
	return settings.AgentProfile{}, fmt.Errorf("profile must be one of %s", strings.Join(ids, ", "))
}

func (t *Toolbox) team(ctx context.Context, call transcript.ToolCall) transcript.ToolOutcome {
	if ctx.Err() != nil {
		return transcript.Cancelled("Cancelled before the team tool started.")
	}
	var text string
	var err error
	switch call.Name {
	case CreateAgent:
		var args CreateAgentArgs
		args, _, err = t.parseCreateAgent(call.Arguments)
		if err == nil {
			text, err = t.Team.CreateAgent(ctx, args)
		}
	case SendMessage:
		var args struct {
			To   string `json:"to"`
			Text string `json:"text"`
		}
		err = decodeTeamArguments(call.Arguments, &args)
		if err == nil && args.To != "orchestrator" && !validAgentName(args.To) {
			err = errors.New("to must be a worker name or orchestrator")
		}
		if err == nil && strings.TrimSpace(args.Text) == "" {
			err = errors.New("text is required")
		}
		if err == nil {
			text, err = t.Team.SendMessage(ctx, args.To, args.Text)
		}
	case RemoveAgent:
		var args struct {
			Name string `json:"name"`
		}
		err = decodeTeamArguments(call.Arguments, &args)
		if err == nil && !validAgentName(args.Name) {
			err = errors.New("name must be a worker name")
		}
		if err == nil {
			text, err = t.Team.RemoveAgent(ctx, args.Name)
		}
	case ListAgents, Wait:
		err = decodeTeamArguments(call.Arguments, &struct{}{})
		if err == nil {
			if call.Name == ListAgents {
				text, err = t.Team.ListAgents(ctx)
			} else {
				text, err = t.Team.Wait(ctx)
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		return transcript.Cancelled("Team tool cancelled.")
	}
	if len(text) > outputLimit {
		text = truncate(text, bodyLimit) + "\nTeam result truncated."
	}
	return bounded(text, nil, err)
}
