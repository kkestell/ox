package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/creachadair/jrpc2"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

// Prompt runs one turn inline on the handler goroutine. Its updates go out
// through jrpc2's Notify, which encodes and writes them before it returns, so
// every update is on the wire before this handler's response.
func (a *Agent) Prompt(
	ctx context.Context,
	request acp.PromptRequest,
) (acp.PromptResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	value := a.findSession(request.SessionID)
	if value == nil {
		return acp.PromptResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams,
			"unknown session %s",
			request.SessionID,
		)
	}
	runCtx, release, err := value.claim(ctx)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	defer release()
	historyStart := value.appendMessage(promptMessage(request.Prompt))

	server := jrpc2.ServerFromContext(ctx)
	answerID, thoughtID := randomID(), randomID()
	var notifyErr error
	notify := func(update, messageID, text string) {
		if notifyErr != nil {
			return
		}
		// The handler's context, not the turn's: an update sent after a
		// cancellation still has to reach the client before the response.
		notifyErr = server.Notify(ctx, "session/update", acp.SessionNotification{
			SessionID: value.id,
			Update: acp.ContentChunk{
				SessionUpdate: update,
				Content:       acp.ContentBlock{Type: "text", Text: text},
				MessageID:     messageID,
			},
		})
	}

	a.logger.Info("prompt started", "session_id", value.id, "model", a.model)
	completion, streamErr := a.client.Stream(runCtx, openrouter.Request{
		Model:    a.model,
		Messages: value.messages(),
	}, func(delta openrouter.Delta) {
		switch delta.Kind {
		case openrouter.DeltaText:
			notify(acp.SessionUpdateAgentMessageChunk, answerID, delta.Text)
		case openrouter.DeltaReasoning:
			notify(acp.SessionUpdateAgentThoughtChunk, thoughtID, delta.Text)
		}
	})

	cancelledByClient := release()
	if notifyErr != nil {
		return acp.PromptResponse{}, fmt.Errorf("send session update: %w", notifyErr)
	}
	if cancelledByClient {
		// The client has already displayed what streamed, so keeping it out of
		// the history would leave the model's view of the conversation behind
		// the user's.
		if completion.Text != "" {
			value.appendMessage(assistantMessage(completion.Text))
		}
		a.logger.Info("prompt cancelled", "session_id", value.id)
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if streamErr != nil {
		if ctx.Err() != nil {
			return acp.PromptResponse{}, jrpc2.Errorf(
				acp.ErrCodeRequestCancelled,
				"request cancelled",
			)
		}
		return acp.PromptResponse{}, fmt.Errorf("run prompt turn: %w", streamErr)
	}

	reason := stopReason(completion.FinishReason)
	if reason == acp.StopReasonRefusal {
		// ACP defines refusal to exclude the refused prompt and everything after
		// it from the next model request.
		value.truncateHistory(historyStart)
	} else {
		value.appendMessage(assistantMessage(completion.Text))
	}
	a.logger.Info("prompt stopped", "session_id", value.id, "stop_reason", reason)
	return acp.PromptResponse{StopReason: reason}, nil
}

// promptMessage renders the prompt as one user message. Its blocks have already
// passed validation, so an unsupported type here is a bug.
func promptMessage(blocks []acp.ContentBlock) openrouter.Message {
	content := make([]openrouter.ContentBlock, len(blocks))
	for index, block := range blocks {
		switch block.Type {
		case "text":
			content[index] = openrouter.ContentBlock{Type: "text", Text: block.Text}
		case "resource_link":
			content[index] = openrouter.ContentBlock{
				Type: "text",
				Text: "[" + markdownText(block.Name) + "](" + markdownTarget(block.URI) + ")",
			}
		default:
			panic("unsupported prompt content type " + block.Type)
		}
	}
	return openrouter.Message{Role: openrouter.RoleUser, Content: content}
}

func assistantMessage(text string) openrouter.Message {
	return openrouter.Message{
		Role:    openrouter.RoleAssistant,
		Content: []openrouter.ContentBlock{{Type: "text", Text: text}},
	}
}

func markdownText(value string) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(value)
}

func markdownTarget(value string) string {
	return strings.ReplaceAll(value, ")", `\)`)
}

func stopReason(reason string) acp.StopReason {
	switch reason {
	case "length":
		return acp.StopReasonMaxTokens
	case "content_filter", "refusal":
		return acp.StopReasonRefusal
	default:
		return acp.StopReasonEndTurn
	}
}
