package agent

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
	if problem := a.credentialProblem(); problem != "" {
		return acp.PromptResponse{}, authRequiredError(problem)
	}
	message, err := promptMessage(request.Prompt)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	runCtx, release, err := value.claim(ctx)
	if err != nil {
		return acp.PromptResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	defer release()
	historyStart := value.appendMessage(message)

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

	a.logger.Info("prompt started", "session_id", value.id, "model", value.configuration.Model)
	completion, streamErr := a.client.Stream(runCtx, openrouter.Request{
		Model:    value.configuration.Model,
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

// promptMessage translates a validated ACP prompt into one provider message.
func promptMessage(blocks []acp.ContentBlock) (openrouter.Message, error) {
	content := make([]openrouter.ContentBlock, len(blocks))
	for index, block := range blocks {
		switch block.Type {
		case "text":
			content[index] = openrouter.ContentBlock{Type: "text", Text: block.Text}
		case "image":
			content[index] = openrouter.ContentBlock{
				Type:     "image_url",
				ImageURL: dataURL(block.MIMEType, block.Data),
			}
		case "audio":
			content[index] = openrouter.ContentBlock{
				Type:        "input_audio",
				AudioData:   block.Data,
				AudioFormat: audioFormat(block.MIMEType),
			}
		case "resource_link":
			content[index] = openrouter.ContentBlock{
				Type: "text",
				Text: "[" + markdownText(block.Name) + "](" + markdownTarget(block.URI) + ")",
			}
		case "resource":
			resource := block.Resource
			if resource.Text != nil {
				marker := fence(*resource.Text)
				content[index] = openrouter.ContentBlock{
					Type: "text",
					Text: "[" + resourceLabel(resource.URI) + "]\n" +
						marker + "\n" + *resource.Text + "\n" + marker,
				}
				continue
			}
			switch media := mediaType(resource.MIMEType); {
			case strings.HasPrefix(media, "image/"):
				content[index] = openrouter.ContentBlock{
					Type:     "image_url",
					ImageURL: dataURL(resource.MIMEType, *resource.Blob),
				}
			case strings.HasPrefix(media, "audio/"):
				content[index] = openrouter.ContentBlock{
					Type:        "input_audio",
					AudioData:   *resource.Blob,
					AudioFormat: audioFormat(resource.MIMEType),
				}
			case media == "":
				return openrouter.Message{}, fmt.Errorf(
					"prompt content block %d resource blob has no MIME type",
					index+1,
				)
			default:
				return openrouter.Message{}, fmt.Errorf(
					"prompt content block %d resource blob with MIME type %q cannot be sent to the model",
					index+1,
					resource.MIMEType,
				)
			}
		default:
			return openrouter.Message{}, fmt.Errorf(
				"prompt content block %d has unsupported type %q",
				index+1,
				block.Type,
			)
		}
	}
	return openrouter.Message{Role: openrouter.RoleUser, Content: content}, nil
}

// mediaType is a MIME type reduced to the part worth comparing: MIME types are
// case insensitive, and the parameters a client copies from a Content-Type
// header say nothing about which model input the payload becomes.
func mediaType(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func dataURL(mimeType, data string) string {
	return "data:" + mediaType(mimeType) + ";base64," + data
}

func audioFormat(mimeType string) string {
	_, subtype, _ := strings.Cut(mediaType(mimeType), "/")
	subtype = strings.TrimPrefix(subtype, "x-")
	switch subtype {
	case "mpeg":
		return "mp3"
	case "wave":
		return "wav"
	default:
		return subtype
	}
}

func resourceLabel(uri string) string {
	parsed, err := url.Parse(uri)
	// A file URI Go parses as opaque carries no path to name the source with, so
	// the URI itself is the most informative label left.
	if err != nil || parsed.Scheme != "file" || parsed.Path == "" {
		return uri
	}

	label := parsed.Path
	fragment := parsed.Fragment
	if strings.HasPrefix(fragment, "L") {
		lineRange := strings.Split(strings.TrimPrefix(fragment, "L"), "-L")
		start, startErr := strconv.Atoi(lineRange[0])
		if startErr == nil && start > 0 {
			switch len(lineRange) {
			case 1:
				label += ":" + strconv.Itoa(start)
			case 2:
				end, endErr := strconv.Atoi(lineRange[1])
				if endErr == nil && end > 0 {
					label += ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
				}
			}
		}
	}
	return label
}

func fence(text string) string {
	longest, run := 0, 0
	for _, character := range text {
		if character == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
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
