package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	protocol "github.com/coder/acp-go-sdk"

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
func promptMessage(blocks []protocol.ContentBlock) (transcript.UserMessage, *acp.Error) {
	var message transcript.UserMessage
	images, imageBytes := 0, 0
	for _, block := range blocks {
		switch {
		case block.Text != nil:
			message.Parts = append(message.Parts, transcript.UserMessagePart{Text: block.Text.Text})
		case block.ResourceLink != nil:
			message.Parts = append(message.Parts, transcript.UserMessagePart{Text: fmt.Sprintf("Resource link: %s\nURI: %s", block.ResourceLink.Name, block.ResourceLink.Uri)})
		case block.Image != nil:
			image := block.Image
			images++
			if images > maxImages {
				return message, acp.InvalidParams("prompt contains too many images")
			}
			switch image.MimeType {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return message, acp.InvalidParams("unsupported image MIME type")
			}
			if len(image.Data) > (maxImageBytes-imageBytes+2)/3*4+4 {
				return message, acp.InvalidParams("prompt images exceed 10 MiB")
			}
			decoded, err := base64.StdEncoding.DecodeString(image.Data)
			if err != nil {
				return message, acp.InvalidParams("image data is not valid base64")
			}
			if len(decoded) == 0 || len(decoded) > maxImageBytes-imageBytes {
				return message, acp.InvalidParams("prompt images exceed 10 MiB")
			}
			imageBytes += len(decoded)
			message.Parts = append(message.Parts, transcript.UserMessagePart{
				Image: &transcript.ImageAttachment{Data: image.Data, MimeType: image.MimeType},
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

func availableCommands(catalog []skills.Skill) protocol.SessionAvailableCommandsUpdate {
	update := protocol.SessionAvailableCommandsUpdate{SessionUpdate: "available_commands_update", AvailableCommands: []protocol.AvailableCommand{}}
	for _, skill := range catalog {
		command := protocol.AvailableCommand{Name: skill.Name, Description: skill.Description}
		if skill.ArgumentHint != "" {
			command.Input = &protocol.AvailableCommandInput{Unstructured: &protocol.UnstructuredCommandInput{Hint: skill.ArgumentHint}}
		}
		update.AvailableCommands = append(update.AvailableCommands, command)
	}
	return update
}

func configOptions(cat catalog.Catalog, selected settings.Settings) []protocol.SessionConfigOption {
	var models, efforts, modes []protocol.SessionConfigSelectOption
	for _, model := range cat {
		choice := protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueId(model.QualifiedID()), Name: model.Name, Meta: map[string]any{
			"contextLimit": model.ContextLimit,
			"inputPrice":   model.InputPrice,
			"outputPrice":  model.OutputPrice,
			"provider":     "OpenRouter",
		}}
		if model.AcceptsImages {
			choice.Description = protocol.Ptr("Accepts images")
		}
		models = append(models, choice)
	}
	for _, effort := range cat.Lookup(selected.Model).Efforts {
		efforts = append(efforts, protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueId(effort), Name: effort.Name()})
	}
	for _, mode := range transcript.Modes {
		modes = append(modes, protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueId(mode), Name: mode.Name(), Description: protocol.Ptr(mode.Description())})
	}
	return []protocol.SessionConfigOption{
		selectOption("model", "Model", "model", selected.Model, models),
		selectOption("effort", "Effort", "thought_level", string(selected.Effort), efforts),
		selectOption("mode", "Mode", "mode", string(selected.Mode), modes),
	}
}

func selectOption(id, name, category, value string, choices []protocol.SessionConfigSelectOption) protocol.SessionConfigOption {
	options := protocol.SessionConfigSelectOptionsUngrouped(choices)
	return protocol.SessionConfigOption{Select: &protocol.SessionConfigOptionSelect{
		Id: protocol.SessionConfigId(id), Name: name, Category: protocol.Ptr(protocol.SessionConfigOptionCategory(category)), Type: "select",
		CurrentValue: protocol.SessionConfigValueId(value), Options: protocol.SessionConfigSelectOptions{Ungrouped: &options},
	}}
}

func optionalText(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

func chunk(kind, text string) protocol.SessionUpdate {
	switch kind {
	case "user_message_chunk":
		return protocol.UpdateUserMessageText(text)
	case "agent_message_chunk":
		return protocol.UpdateAgentMessageText(text)
	case "agent_thought_chunk":
		return protocol.UpdateAgentThoughtText(text)
	}
	panic("unknown message chunk " + kind)
}

func usageUpdate(used uint64, size int, cost *float64) protocol.SessionUsageUpdate {
	update := protocol.SessionUsageUpdate{SessionUpdate: "usage_update", Used: int(used), Size: size}
	if cost != nil {
		update.Cost = &protocol.Cost{Amount: *cost, Currency: "USD"}
	}
	return update
}

func toolCall(call transcript.ToolCall) acp.ToolCall {
	return acp.ToolCall{
		SessionUpdateToolCall: protocol.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: protocol.ToolCallId(call.CallID), Title: tools.Title(call),
			Kind: toolKind(call.Name), RawInput: rawInput(call),
		},
		Name: call.Name,
	}
}

// update converts a turn event to an ACP session update.
func update(event agent.Event) any {
	switch event := event.(type) {
	case agent.SessionInfo:
		return protocol.SessionSessionInfoUpdate{SessionUpdate: "session_info_update", Title: optionalText(event.Title), UpdatedAt: optionalText(event.UpdatedAt)}
	case agent.TextDelta:
		return chunk("agent_message_chunk", string(event))
	case agent.ReasoningDelta:
		return chunk("agent_thought_chunk", string(event))
	case agent.ToolPending:
		return toolCall(event.Call)
	case agent.ToolStarted:
		return protocol.SessionToolCallUpdate{SessionUpdate: "tool_call_update", ToolCallId: protocol.ToolCallId(event.Call.CallID), Status: protocol.Ptr(protocol.ToolCallStatusInProgress)}
	case agent.ToolFinished:
		return protocol.SessionToolCallUpdate{
			SessionUpdate: "tool_call_update", ToolCallId: protocol.ToolCallId(event.Call.CallID), Status: protocol.Ptr(status(event.Outcome)),
			Content: outputContent(event.Outcome), RawOutput: event.Outcome.Text,
		}
	case agent.Usage:
		return usageUpdate(event.Used, event.Size, event.Cost)
	}
	panic(fmt.Sprintf("unknown turn event %T", event))
}

// toolKind is what an ACP client uses to pick an icon for a call. Other, the
// default, is omitted.
func toolKind(name string) protocol.ToolKind {
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

func status(outcome transcript.ToolOutcome) protocol.ToolCallStatus {
	if outcome.Status == transcript.ToolCompleted {
		return "completed"
	}
	return "failed"
}

func textContent(text string) protocol.ToolCallContent {
	return protocol.ToolContent(protocol.TextBlock(text))
}

// outputContent is the outcome's content blocks, or its text when it has none.
func outputContent(outcome transcript.ToolOutcome) []protocol.ToolCallContent {
	if len(outcome.Content) == 0 {
		return []protocol.ToolCallContent{textContent(outcome.Text)}
	}
	var content []protocol.ToolCallContent
	for _, item := range outcome.Content {
		if item.Diff == nil {
			content = append(content, textContent(item.Text))
			continue
		}
		content = append(content, protocol.ToolCallContent{Diff: &protocol.ToolCallContentDiff{Type: "diff", Path: item.Diff.Path, OldText: item.Diff.OldText, NewText: item.Diff.NewText}})
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
			updates = append(updates, protocol.SessionUpdateToolCall{
				SessionUpdate: "tool_call", ToolCallId: protocol.ToolCallId("turn-error-" + rand.Text()), Title: "Turn error", Status: "failed",
				Content: []protocol.ToolCallContent{textContent(string(entry))}, RawOutput: string(entry),
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
				update := toolCall(call)
				update.Status, update.Content, update.RawOutput = status(outcome), outputContent(outcome), outcome.Text
				updates = append(updates, update)
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

func imageChunk(image transcript.ImageAttachment) protocol.SessionUpdate {
	return protocol.UpdateUserMessage(protocol.ImageBlock(image.Data, image.MimeType))
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
		RequestPermissionRequest: protocol.RequestPermissionRequest{
			SessionId: protocol.SessionId(sessionID),
			Options: []protocol.PermissionOption{
				{OptionId: "approve", Name: "Yes", Kind: "allow_once"},
				{OptionId: "deny", Name: "No", Kind: "reject_once"},
			},
		},
		ToolCall: acp.PermissionToolCall{
			ToolCallUpdate: protocol.ToolCallUpdate{
				ToolCallId: protocol.ToolCallId(call.CallID), Title: protocol.Ptr(tools.Title(call)), Kind: protocol.Ptr(toolKind(call.Name)),
				Status: protocol.Ptr(protocol.ToolCallStatusPending), RawInput: input, Content: []protocol.ToolCallContent{textContent(content)},
			},
			Name: call.Name,
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
