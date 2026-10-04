package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/settings"
	"ox/internal/skills"
	"ox/internal/tools"
	"ox/internal/transcript"
)

const (
	maxImages     = 4
	maxImageBytes = 10 * 1024 * 1024
)

// promptMessage converts prompt content to a user message, keeping text and
// image order. Resource links contribute text and are not fetched.
func promptMessage(blocks []acp.ContentBlock) (transcript.UserMessage, *acp.Error) {
	var message transcript.UserMessage
	images, imageBytes := 0, 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			message.Parts = append(message.Parts, transcript.UserMessagePart{Text: block.Text})
		case "resource_link":
			message.Parts = append(message.Parts, transcript.UserMessagePart{Text: fmt.Sprintf("Resource link: %s\nURI: %s", block.Name, block.URI)})
		case "image":
			images++
			if images > maxImages {
				return message, acp.InvalidParams("prompt contains too many images")
			}
			switch block.MimeType {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return message, acp.InvalidParams("unsupported image MIME type")
			}
			if len(block.Data) > (maxImageBytes-imageBytes+2)/3*4+4 {
				return message, acp.InvalidParams("prompt images exceed 10 MiB")
			}
			decoded, err := base64.StdEncoding.DecodeString(block.Data)
			if err != nil {
				return message, acp.InvalidParams("image data is not valid base64")
			}
			if len(decoded) == 0 || len(decoded) > maxImageBytes-imageBytes {
				return message, acp.InvalidParams("prompt images exceed 10 MiB")
			}
			imageBytes += len(decoded)
			message.Parts = append(message.Parts, transcript.UserMessagePart{
				Image: &transcript.ImageAttachment{Data: block.Data, MimeType: block.MimeType},
			})
		default:
			return message, acp.InvalidParams("prompts may contain only text, resource links, and images")
		}
	}
	if strings.TrimSpace(message.Text()) == "" && !message.HasImages() {
		return message, acp.InvalidParams("prompt contains no text or image")
	}
	return message, nil
}

// dispatch makes a prompt whose first word is `/<name>` for a catalog skill a
// skill invocation; the rest of its text, trimmed, is literal skill arguments.
// Any other text is a user message.
func dispatch(message transcript.UserMessage, catalog []skills.Skill) transcript.TurnInput {
	text := strings.TrimSpace(message.Text())
	word, rest := text, ""
	if index := strings.IndexFunc(text, unicode.IsSpace); index >= 0 {
		word, rest = text[:index], text[index:]
	}
	if name, ok := strings.CutPrefix(word, "/"); ok {
		for _, skill := range catalog {
			if skill.Name != name {
				continue
			}
			invocation := &transcript.SkillInvocation{Name: skill.Name, Arguments: strings.TrimSpace(rest), Instructions: skill.Instructions}
			for _, part := range message.Parts {
				if part.Image != nil {
					invocation.Images = append(invocation.Images, *part.Image)
				}
			}
			return transcript.TurnInput{Skill: invocation}
		}
	}
	return transcript.TurnInput{Message: &message}
}

func availableCommands(catalog []skills.Skill) acp.AvailableCommandsUpdate {
	update := acp.AvailableCommandsUpdate{SessionUpdate: "available_commands_update", AvailableCommands: []acp.AvailableCommand{}}
	for _, skill := range catalog {
		command := acp.AvailableCommand{Name: skill.Name, Description: skill.Description}
		if skill.ArgumentHint != "" {
			command.Input = &acp.CommandInput{Hint: skill.ArgumentHint}
		}
		update.AvailableCommands = append(update.AvailableCommands, command)
	}
	return update
}

func configOptions(cat catalog.Catalog, selected settings.Settings) []acp.ConfigOption {
	var models, efforts, modes []acp.ConfigChoice
	for _, model := range cat {
		choice := acp.ConfigChoice{Value: model.QualifiedID(), Name: model.Name, Meta: map[string]any{
			"contextLimit": model.ContextLimit,
			"inputPrice":   model.InputPrice,
			"outputPrice":  model.OutputPrice,
			"provider":     "OpenRouter",
		}}
		if model.AcceptsImages {
			choice.Description = "Accepts images"
		}
		models = append(models, choice)
	}
	for _, effort := range cat.Lookup(selected.Model).Efforts {
		efforts = append(efforts, acp.ConfigChoice{Value: string(effort), Name: effort.Name()})
	}
	for _, mode := range transcript.Modes {
		modes = append(modes, acp.ConfigChoice{Value: string(mode), Name: mode.Name(), Description: mode.Description()})
	}
	return []acp.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", Type: "select", CurrentValue: selected.Model, Options: models},
		{ID: "effort", Name: "Effort", Category: "thought_level", Type: "select", CurrentValue: string(selected.Effort), Options: efforts},
		{ID: "mode", Name: "Mode", Category: "mode", Type: "select", CurrentValue: string(selected.Mode), Options: modes},
	}
}

func chunk(kind, text string) acp.Chunk {
	return acp.Chunk{SessionUpdate: kind, Content: acp.TextBlock(text)}
}

func usageUpdate(used uint64, size int, cost *float64) acp.UsageUpdate {
	update := acp.UsageUpdate{SessionUpdate: "usage_update", Used: used, Size: size}
	if cost != nil {
		update.Cost = &acp.Cost{Amount: *cost, Currency: "USD"}
	}
	return update
}

// update converts a turn event to an ACP session update.
func update(event agent.Event) any {
	switch event := event.(type) {
	case agent.SessionInfo:
		return acp.SessionInfoUpdate{SessionUpdate: "session_info_update", Title: event.Title, UpdatedAt: event.UpdatedAt}
	case agent.TextDelta:
		return chunk("agent_message_chunk", string(event))
	case agent.ReasoningDelta:
		return chunk("agent_thought_chunk", string(event))
	case agent.ToolPending:
		return acp.ToolCall{
			SessionUpdate: "tool_call", ToolCallID: event.Call.CallID, Title: tools.Title(event.Call),
			Name: event.Call.Name, Kind: toolKind(event.Call.Name), RawInput: rawInput(event.Call),
		}
	case agent.ToolStarted:
		return acp.ToolCall{SessionUpdate: "tool_call_update", ToolCallID: event.Call.CallID, Status: "in_progress"}
	case agent.ToolFinished:
		return acp.ToolCall{
			SessionUpdate: "tool_call_update", ToolCallID: event.Call.CallID, Status: status(event.Outcome),
			Content: outputContent(event.Outcome), RawOutput: event.Outcome.Text,
		}
	case agent.Usage:
		return usageUpdate(event.Used, event.Size, event.Cost)
	}
	panic(fmt.Sprintf("unknown turn event %T", event))
}

// toolKind is what an ACP client uses to pick an icon for a call. Other, the
// default, is omitted.
func toolKind(name string) string {
	switch name {
	case tools.Shell, tools.ShellProcess:
		return "execute"
	case tools.ReadFile:
		return "read"
	case tools.Glob, tools.Grep:
		return "search"
	case tools.ApplyPatch:
		return "edit"
	}
	return ""
}

func rawInput(call transcript.ToolCall) any {
	var input any
	if json.Unmarshal([]byte(call.Arguments), &input) != nil {
		return call.Arguments
	}
	return input
}

func status(outcome transcript.ToolOutcome) string {
	if outcome.Status == transcript.ToolCompleted {
		return "completed"
	}
	return "failed"
}

func textContent(text string) acp.ToolCallContent {
	block := acp.TextBlock(text)
	return acp.ToolCallContent{Type: "content", Content: &block}
}

// outputContent is the outcome's content blocks, or its text when it has none.
func outputContent(outcome transcript.ToolOutcome) []acp.ToolCallContent {
	if len(outcome.Content) == 0 {
		return []acp.ToolCallContent{textContent(outcome.Text)}
	}
	var content []acp.ToolCallContent
	for _, item := range outcome.Content {
		if item.Diff == nil {
			content = append(content, textContent(item.Text))
			continue
		}
		newText := item.Diff.NewText
		content = append(content, acp.ToolCallContent{Type: "diff", Path: item.Diff.Path, OldText: item.Diff.OldText, NewText: &newText})
	}
	return content
}

// replay sends the saved transcript as displayable content and final tool
// states. The model and continuation metadata are never shown. A saved turn
// error is shown as a failed tool call, so it stays apart from model text.
func replay(entries []transcript.Entry, send func(any) error) error {
	for _, entry := range entries {
		var updates []any
		switch entry := entry.(type) {
		case *transcript.TurnStart:
			if skill := entry.Input.Skill; skill != nil {
				updates = append(updates, chunk("user_message_chunk", skill.CommandText()))
				for _, image := range skill.Images {
					updates = append(updates, imageChunk(image))
				}
				break
			}
			for _, part := range entry.Input.Message.Parts {
				if part.Image != nil {
					updates = append(updates, imageChunk(*part.Image))
				} else {
					updates = append(updates, chunk("user_message_chunk", part.Text))
				}
			}
		case transcript.TurnError:
			updates = append(updates, acp.ToolCall{
				SessionUpdate: "tool_call", ToolCallID: "turn-error-" + rand.Text(), Title: "Turn error", Status: "failed",
				Content: []acp.ToolCallContent{textContent(string(entry))}, RawOutput: string(entry),
			})
		case *transcript.AssistantBatch:
			if entry.Message.Reasoning != "" {
				updates = append(updates, chunk("agent_thought_chunk", entry.Message.Reasoning))
			}
			if entry.Message.Text != "" {
				updates = append(updates, chunk("agent_message_chunk", entry.Message.Text))
			}
			for i, call := range entry.Message.ToolCalls {
				outcome := entry.Outcomes[i]
				updates = append(updates, acp.ToolCall{
					SessionUpdate: "tool_call", ToolCallID: call.CallID, Title: tools.Title(call), Name: call.Name,
					Kind: toolKind(call.Name), Status: status(outcome), Content: outputContent(outcome),
					RawInput: rawInput(call), RawOutput: outcome.Text,
				})
			}
		}
		for _, update := range updates {
			if err := send(update); err != nil {
				return err
			}
		}
	}
	return nil
}

func imageChunk(image transcript.ImageAttachment) acp.Chunk {
	return acp.Chunk{SessionUpdate: "user_message_chunk", Content: acp.ContentBlock{Type: "image", Data: image.Data, MimeType: image.MimeType}}
}

// permissionRequest asks whether one shell command may run or one input may
// be sent to a background process. Clients can omit rawInput from the
// approval UI, so the content shows everything being approved.
func permissionRequest(sessionID, workspace string, call transcript.ToolCall, permission tools.Permission) acp.RequestPermissionRequest {
	input := rawInput(call)
	var content string
	switch permission.Kind {
	case tools.PermissionCommand:
		label, text := "Arguments", call.Arguments
		if arguments, ok := input.(map[string]any); ok {
			if command, ok := arguments["command"].(string); ok {
				label, text = "Command", command
			}
		}
		content = fmt.Sprintf("Working directory: %s\n\n%s:\n\n%s", workspace, label, indented(text))
	case tools.PermissionInput:
		command := "    (no shell process with this ID)"
		if permission.Command != "" {
			command = indented(permission.Command)
		}
		text := "    (none)"
		if permission.Text != "" {
			text = indented(permission.Text)
		}
		closes := "no"
		if permission.CloseStdin {
			closes = "yes"
		}
		content = fmt.Sprintf("Shell process: %s\n\nCommand:\n\n%s\n\nInput:\n\n%s\n\nCloses stdin afterward: %s",
			permission.ProcessID, command, text, closes)
	}
	return acp.RequestPermissionRequest{
		SessionID: sessionID,
		ToolCall: acp.ToolCall{
			ToolCallID: call.CallID, Title: tools.Title(call), Name: call.Name, Kind: toolKind(call.Name),
			Status: "pending", RawInput: input, Content: []acp.ToolCallContent{textContent(content)},
		},
		Options: []acp.PermissionOption{
			{OptionID: "approve", Name: "Yes", Kind: "allow_once"},
			{OptionID: "deny", Name: "No", Kind: "reject_once"},
		},
	}
}

func indented(text string) string {
	var out strings.Builder
	for line := range strings.Lines(text) {
		out.WriteString("    " + line)
	}
	return out.String()
}
