package e2e

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func textPrompt(text string) []acp.ContentBlock {
	return []acp.ContentBlock{{Type: "text", Text: text}}
}

func e2ePointer(value string) *string {
	return &value
}

// prompt runs one turn to completion, which is all a test needs when what it
// asserts is what ox sent the model rather than what it streamed back.
func prompt(t *testing.T, child *process, session, text string) {
	t.Helper()
	result := child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt(text),
	})
	if response := promptResponse(t, result); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
}

func promptResponse(t *testing.T, result json.RawMessage) acp.PromptResponse {
	t.Helper()
	var response acp.PromptResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("decode session/prompt result: %v", err)
	}
	return response
}

// updates decodes one session's session/update notifications ox sent before
// the response the caller has already read. Other sessions' notifications stay
// buffered for their own caller.
type sessionUpdate struct {
	SessionUpdate string                    `json:"sessionUpdate"`
	Content       acp.ContentBlock          `json:"content"`
	MessageID     string                    `json:"messageId"`
	ToolCallID    string                    `json:"toolCallId"`
	Status        acp.ToolCallStatus        `json:"status"`
	Locations     []acp.ToolCallLocation    `json:"locations"`
	Used          uint64                    `json:"used"`
	Size          uint64                    `json:"size"`
	Cost          *acp.Cost                 `json:"cost"`
	Meta          acp.Metadata              `json:"_meta"`
	ConfigOptions []acp.SessionConfigOption `json:"configOptions"`
	Entries       []acp.PlanEntry           `json:"entries"`
}

func (u *sessionUpdate) UnmarshalJSON(data []byte) error {
	var wire struct {
		SessionUpdate string                    `json:"sessionUpdate"`
		Content       json.RawMessage           `json:"content"`
		MessageID     string                    `json:"messageId"`
		ToolCallID    string                    `json:"toolCallId"`
		Status        acp.ToolCallStatus        `json:"status"`
		Locations     []acp.ToolCallLocation    `json:"locations"`
		Used          uint64                    `json:"used"`
		Size          uint64                    `json:"size"`
		Cost          *acp.Cost                 `json:"cost"`
		Meta          acp.Metadata              `json:"_meta"`
		ConfigOptions []acp.SessionConfigOption `json:"configOptions"`
		Entries       []acp.PlanEntry           `json:"entries"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	u.SessionUpdate = wire.SessionUpdate
	u.MessageID = wire.MessageID
	u.ToolCallID = wire.ToolCallID
	u.Status = wire.Status
	u.Locations = wire.Locations
	u.Used = wire.Used
	u.Size = wire.Size
	u.Cost = wire.Cost
	u.Meta = wire.Meta
	u.ConfigOptions = wire.ConfigOptions
	u.Entries = wire.Entries
	if len(wire.Content) > 0 && wire.Content[0] == '{' {
		return json.Unmarshal(wire.Content, &u.Content)
	}
	if len(wire.Content) > 0 && wire.Content[0] == '[' {
		var content []acp.ToolCallContent
		if err := json.Unmarshal(wire.Content, &content); err != nil {
			return err
		}
		if len(content) > 0 {
			u.Content = content[0].Content
		}
	}
	return nil
}

type sessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

func updates(t *testing.T, child *process, session string) []sessionNotification {
	t.Helper()
	var decoded []sessionNotification
	var remaining []message
	for _, notification := range child.pending {
		if notification.Method != "session/update" || len(notification.ID) != 0 {
			remaining = append(remaining, notification)
			continue
		}
		var update sessionNotification
		if err := json.Unmarshal(notification.Params, &update); err != nil {
			t.Fatalf("decode session/update params: %v", err)
		}
		if update.SessionID != session {
			remaining = append(remaining, notification)
			continue
		}
		decoded = append(decoded, update)
	}
	child.pending = remaining
	return decoded
}

// chunks concatenates the text of the updates of one kind and returns the
// message ID they all carry.
func chunks(
	t *testing.T,
	sent []sessionNotification,
	kind string,
) (string, string) {
	t.Helper()
	var text strings.Builder
	var messageID string
	seen := 0
	for _, update := range sent {
		if update.Update.SessionUpdate != kind {
			continue
		}
		if update.Update.Content.Type != "text" {
			t.Fatalf("%s content type = %q, want text", kind, update.Update.Content.Type)
		}
		if update.Update.Content.Text == "" {
			t.Fatalf("%s carried no text", kind)
		}
		if seen == 0 {
			messageID = update.Update.MessageID
			if messageID == "" {
				t.Fatalf("%s carried no message ID", kind)
			}
		} else if update.Update.MessageID != messageID {
			t.Fatalf("%s message ID = %q, want %q", kind, update.Update.MessageID, messageID)
		}
		seen++
		text.WriteString(update.Update.Content.Text)
	}
	return text.String(), messageID
}

type exchange struct {
	role string
	text string
}

func assertConversation(t *testing.T, messages []modelMessage, want []exchange) {
	t.Helper()
	if len(messages) > 0 && messages[0].Role == "system" {
		messages = messages[1:]
	}
	if len(messages) != len(want) {
		t.Fatalf("model received %d messages, want %d: %#v", len(messages), len(want), messages)
	}
	for index, message := range messages {
		if message.Role != want[index].role || message.text() != want[index].text {
			t.Fatalf("message %d = %s %q, want %s %q",
				index, message.Role, message.text(), want[index].role, want[index].text)
		}
	}
}

func TestPromptStreamsTheAnswerBeforeTheResponse(t *testing.T) {
	model := startModel(t, sse(evText("Hello, "), evText(""), evText("world"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))

	response := promptResponse(t, child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	}))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Errorf("stopReason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}

	sent := updates(t, child, session)
	if len(sent) != 2 {
		t.Fatalf("agent sent %d updates, want 2: %#v", len(sent), sent)
	}
	answer, _ := chunks(t, sent, acp.SessionUpdateAgentMessageChunk)
	if answer != "Hello, world" {
		t.Errorf("answer = %q, want %q", answer, "Hello, world")
	}

	requests := model.requests()
	if len(requests) != 1 {
		t.Fatalf("model received %d requests, want 1", len(requests))
	}
	if requests[0].Model != "test/model" || !requests[0].Stream {
		t.Errorf("request = %#v, want a stream of test/model", requests[0])
	}
	assertConversation(t, requests[0].Messages, []exchange{{role: "user", text: "hi"}})
}

func TestPromptStreamsReasoningUnderItsOwnMessageID(t *testing.T) {
	model := startModel(t, sse(
		evReasoning("thinking "),
		evReasoning("hard"),
		evText("answer"),
		evFinishReason("stop"),
	))
	child, session := startSession(t, withModel(model))

	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	})

	sent := updates(t, child, session)
	if len(sent) != 3 {
		t.Fatalf("agent sent %d updates, want 3: %#v", len(sent), sent)
	}
	if sent[0].Update.SessionUpdate != acp.SessionUpdateAgentThoughtChunk {
		t.Errorf("first update = %q, want %q",
			sent[0].Update.SessionUpdate, acp.SessionUpdateAgentThoughtChunk)
	}
	thought, thoughtID := chunks(t, sent, acp.SessionUpdateAgentThoughtChunk)
	answer, messageID := chunks(t, sent, acp.SessionUpdateAgentMessageChunk)
	if thought != "thinking hard" {
		t.Errorf("thought = %q, want %q", thought, "thinking hard")
	}
	if answer != "answer" {
		t.Errorf("answer = %q, want %q", answer, "answer")
	}
	if thoughtID == messageID {
		t.Errorf("thought and answer share message ID %q", messageID)
	}
}

func TestPromptAccumulatesTheConversation(t *testing.T) {
	model := startModel(t,
		sse(evText("first answer"), evFinishReason("stop")),
		sse(evText("second answer"), evFinishReason("stop")),
	)
	child, session := startSession(t, withModel(model))

	for _, text := range []string{"one", "two"} {
		child.request("session/prompt", acp.PromptRequest{
			SessionID: session,
			Prompt:    textPrompt(text),
		})
		updates(t, child, session)
	}

	requests := model.requests()
	if len(requests) != 2 {
		t.Fatalf("model received %d requests, want 2", len(requests))
	}
	assertConversation(t, requests[1].Messages, []exchange{
		{role: "user", text: "one"},
		{role: "assistant", text: "first answer"},
		{role: "user", text: "two"},
	})
}

func TestPromptRendersResourceLinksAsMarkdown(t *testing.T) {
	model := startModel(t, sse(evText("read it"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))

	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt: []acp.ContentBlock{
			{Type: "text", Text: "look at "},
			{Type: "resource_link", Name: "main.go", URI: "file:///workspace/main.go"},
		},
	})
	updates(t, child, session)

	assertConversation(t, model.requests()[0].Messages, []exchange{
		{role: "user", text: "look at [main.go](file:///workspace/main.go)"},
	})
}

func TestPromptRejectsContentItCannotSize(t *testing.T) {
	model := startModel(t)
	child, session := startSession(t, withModel(model))

	responseError := child.requestError("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt: []acp.ContentBlock{
			{Type: "text", Text: "inspect these"},
			{Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ=="},
			{Type: "audio", MIMEType: "audio/x-wav", Data: "c291bmQ="},
			{Type: "resource_link", Name: `a [file]`, URI: "file:///tmp/a(b).go"},
			{Type: "resource", Resource: &acp.EmbeddedResource{
				URI:  "file:///tmp/context%20file.txt#L12-L14",
				Text: e2ePointer("inside ``` a fence"),
			}},
			{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///tmp/embedded.png", MIMEType: "image/png", Blob: e2ePointer("aW1hZ2U="),
			}},
			{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///tmp/embedded.flac", MIMEType: "audio/flac", Blob: e2ePointer("YXVkaW8="),
			}},
		},
	})
	updates(t, child, session)
	if !strings.Contains(responseError.Message, "cannot size provider request containing image content") {
		t.Fatalf("error = %q", responseError.Message)
	}
	if requests := model.requests(); len(requests) != 0 {
		t.Fatalf("model received unsized request: %#v", requests)
	}
}

func TestRejectedUnsizedPromptDoesNotEnterHistory(t *testing.T) {
	model := startModel(t, sse(evText("second answer"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))

	responseError := child.requestError("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt: []acp.ContentBlock{
			{Type: "text", Text: "describe"},
			{Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ=="},
		},
	})
	if !strings.Contains(responseError.Message, "image content") {
		t.Fatalf("error = %q", responseError.Message)
	}
	updates(t, child, session)
	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("again"),
	})
	updates(t, child, session)

	requests := model.requests()
	if len(requests) != 1 || len(requests[0].Messages) != 2 {
		t.Fatalf("model requests = %#v, want only the retry", requests)
	}
	assertConversation(t, requests[0].Messages, []exchange{{role: "user", text: "again"}})
}

func TestPromptRejectsUnroutableResourcesWithoutOccupyingTheSession(t *testing.T) {
	for _, test := range []struct {
		name     string
		mimeType string
		want     string
	}{
		{name: "missing mime type", want: "no MIME type"},
		{name: "unsupported mime type", mimeType: "application/pdf", want: "application/pdf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := startModel(t, sse(evText("accepted"), evFinishReason("stop")))
			child, session := startSession(t, withModel(model))

			responseError := child.requestError("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///tmp/blob", MIMEType: test.mimeType, Blob: e2ePointer("YmxvYg=="),
				}}},
			})
			if responseError.Code != -32602 || !strings.Contains(responseError.Message, test.want) {
				t.Fatalf("error = %#v, want -32602 mentioning %q", responseError, test.want)
			}

			child.request("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("still free"),
			})
			updates(t, child, session)
			if requests := model.requests(); len(requests) != 1 {
				t.Fatalf("model received %d requests, want 1", len(requests))
			}
		})
	}
}

func TestPromptMapsFinishReasons(t *testing.T) {
	for _, test := range []struct {
		finishReason string
		want         acp.StopReason
	}{
		{finishReason: "stop", want: acp.StopReasonEndTurn},
		{finishReason: "tool_calls", want: acp.StopReasonEndTurn},
		{finishReason: "length", want: acp.StopReasonMaxTokens},
		{finishReason: "content_filter", want: acp.StopReasonRefusal},
		{finishReason: "refusal", want: acp.StopReasonRefusal},
	} {
		t.Run(test.finishReason, func(t *testing.T) {
			model := startModel(t, sse(evText("answer"), evFinishReason(test.finishReason)))
			child, session := startSession(t, withModel(model))

			response := promptResponse(t, child.request("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("hi"),
			}))
			updates(t, child, session)
			if response.StopReason != test.want {
				t.Fatalf("stopReason = %q, want %q", response.StopReason, test.want)
			}
		})
	}
}

func TestPromptDropsARefusedTurnFromTheConversation(t *testing.T) {
	for _, finishReason := range []string{"content_filter", "refusal"} {
		t.Run(finishReason, func(t *testing.T) {
			model := startModel(t,
				sse(evText("refused answer"), evFinishReason(finishReason)),
				sse(evText("accepted answer"), evFinishReason("stop")),
			)
			child, session := startSession(t, withModel(model))

			response := promptResponse(t, child.request("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("refused prompt"),
			}))
			updates(t, child, session)
			if response.StopReason != acp.StopReasonRefusal {
				t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonRefusal)
			}

			child.request("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("next prompt"),
			})
			updates(t, child, session)

			requests := model.requests()
			if len(requests) != 2 {
				t.Fatalf("model received %d requests, want 2", len(requests))
			}
			assertConversation(t, requests[1].Messages, []exchange{
				{role: "user", text: "next prompt"},
			})
		})
	}
}

func TestPromptEndsTheTurnWithoutAFinishReason(t *testing.T) {
	model := startModel(t, sse(evText("answer")))
	child, session := startSession(t, withModel(model))

	response := promptResponse(t, child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	}))
	updates(t, child, session)
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
}

func TestPromptRejectsInvalidRequests(t *testing.T) {
	child, session := startSession(t)
	for _, test := range []struct {
		name    string
		request acp.PromptRequest
		want    string
	}{
		{
			name:    "unknown session",
			request: acp.PromptRequest{SessionID: "missing", Prompt: textPrompt("hi")},
			want:    "unknown session",
		},
		{
			name:    "missing session",
			request: acp.PromptRequest{Prompt: textPrompt("hi")},
			want:    "sessionId",
		},
		{
			name:    "empty prompt",
			request: acp.PromptRequest{SessionID: session},
			want:    "at least one",
		},
		{
			name: "image without mime type",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "image", Data: "aW1hZ2U="}},
			},
			want: "block 1 image requires mimeType",
		},
		{
			name: "image without data",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "image", MIMEType: "image/png"}},
			},
			want: "image requires data",
		},
		{
			name: "image with invalid base64",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "image", MIMEType: "image/png", Data: "%%%"}},
			},
			want: "image data must be standard base64",
		},
		{
			name: "audio without mime type",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "audio", Data: "YXVkaW8="}},
			},
			want: "audio requires mimeType",
		},
		{
			name: "audio without data",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "audio", MIMEType: "audio/wav"}},
			},
			want: "audio requires data",
		},
		{
			name: "audio with invalid base64",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "audio", MIMEType: "audio/wav", Data: "%%%"}},
			},
			want: "audio data must be standard base64",
		},
		{
			name: "resource link without a uri",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "resource_link", Name: "main.go"}},
			},
			want: "resource link requires uri",
		},
		{
			name: "resource without uri",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
					Text: e2ePointer("text"),
				}}},
			},
			want: "resource requires uri",
		},
		{
			name: "resource without payload",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///tmp/empty",
				}}},
			},
			want: "exactly one of text or blob",
		},
		{
			name: "resource with text and blob",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///tmp/both", Text: e2ePointer("text"), Blob: e2ePointer("YmxvYg=="),
				}}},
			},
			want: "exactly one of text or blob",
		},
		{
			name: "resource with invalid blob base64",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///tmp/blob", Blob: e2ePointer("%%%"),
				}}},
			},
			want: "resource blob must be standard base64",
		},
		{
			name: "unknown content type is positioned",
			request: acp.PromptRequest{
				SessionID: session,
				Prompt:    []acp.ContentBlock{{Type: "text"}, {Type: "future"}},
			},
			want: `block 2 has unsupported type "future"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			responseError := child.requestError("session/prompt", test.request)
			if responseError.Code != -32602 {
				t.Fatalf("error code = %d (%s), want -32602",
					responseError.Code, responseError.Message)
			}
			if !strings.Contains(responseError.Message, test.want) {
				t.Fatalf("error message = %q, want it to contain %q",
					responseError.Message, test.want)
			}
		})
	}
}

func TestPromptReportsProviderFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		queue func(*mockModel)
		want  string
	}{
		{
			name: "error chunk",
			queue: func(model *mockModel) {
				for range 5 {
					model.queue(sse(evError(502, "provider unavailable")))
				}
			},
			want: "provider unavailable",
		},
		{
			name: "non-2xx response",
			queue: func(model *mockModel) {
				for range 5 {
					model.fail(http.StatusInternalServerError, "provider exploded")
				}
			},
			want: "provider exploded",
		},
		{
			name:  "no choices",
			queue: func(model *mockModel) { model.queue(sse(evUsage(1, 2, 3))) },
			want:  "no completion choices",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := startModel(t)
			test.queue(model)
			child, session := startSession(t, withModel(model))

			responseError := child.requestError("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("hi"),
			})
			if !strings.Contains(responseError.Message, test.want) {
				t.Fatalf("error message = %q, want it to mention %q",
					responseError.Message, test.want)
			}
		})
	}
}

func TestPromptRefusesASecondConcurrentTurn(t *testing.T) {
	model := startModel(t)
	held := model.hold(frames(evText("first")))
	child, session := startSession(t, withModel(model))

	prompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	})
	held.await(t)
	child.notification("session/update")

	responseError := child.requestError("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("again"),
	})
	if responseError.Code != -32602 {
		t.Fatalf("error code = %d (%s), want -32602",
			responseError.Code, responseError.Message)
	}

	held.finish(sse(evFinishReason("stop")))
	response := promptResponse(t, child.result(child.await(prompt)))
	updates(t, child, session)
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
	if requests := model.requests(); len(requests) != 1 {
		t.Fatalf("model received %d requests, want 1", len(requests))
	}
}

func TestSessionCancelEndsTheTurnMidStream(t *testing.T) {
	model := startModel(t)
	held := model.hold(frames(evText("partial")))
	model.queue(sse(evText("second answer"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))

	prompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	})
	held.await(t)
	// Reading the chunk before cancelling makes what the turn streamed, and so
	// what the history keeps, deterministic.
	chunk := child.notification("session/update")
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})

	response := promptResponse(t, child.result(child.await(prompt)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonCancelled)
	}
	var streamed sessionNotification
	if err := json.Unmarshal(chunk.Params, &streamed); err != nil {
		t.Fatal(err)
	}
	if streamed.Update.Content.Text != "partial" {
		t.Fatalf("streamed %q, want %q", streamed.Update.Content.Text, "partial")
	}

	// The session takes another prompt, and the model sees what the cancelled
	// turn already showed the user.
	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("again"),
	})
	updates(t, child, session)
	requests := model.requests()
	if len(requests) != 2 {
		t.Fatalf("model received %d requests, want 2", len(requests))
	}
	assertConversation(t, requests[1].Messages, []exchange{
		{role: "user", text: "hi"},
		{role: "assistant", text: "partial"},
		{role: "user", text: "again"},
	})
}

func TestSessionCancelEndsTheTurnBeforeTheFirstDelta(t *testing.T) {
	model := startModel(t)
	held := model.hold("")
	child, session := startSession(t, withModel(model))

	prompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	})
	held.await(t)
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})

	response := promptResponse(t, child.result(child.await(prompt)))
	if response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonCancelled)
	}
	if sent := updates(t, child, session); len(sent) != 0 {
		t.Fatalf("agent sent %d updates, want none: %#v", len(sent), sent)
	}
}

func TestRequestCancellationEndsTheTurnWithAnError(t *testing.T) {
	model := startModel(t)
	held := model.hold(frames(evText("partial")))
	child, session := startSession(t, withModel(model))

	prompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("hi"),
	})
	held.await(t)
	child.notification("session/update")
	child.notify("$/cancel_request", struct {
		RequestID json.RawMessage `json:"requestId"`
	}{RequestID: json.RawMessage(strconv.Itoa(prompt.id))})

	responseError := child.failure(child.await(prompt))
	if responseError.Code != acp.ErrCodeRequestCancelled {
		t.Fatalf("error code = %d (%s), want %d",
			responseError.Code, responseError.Message, acp.ErrCodeRequestCancelled)
	}
}

func TestConcurrentSessionsStreamIndependently(t *testing.T) {
	model := startModel(t)
	firstHeld := model.holdFor("first prompt", frames(evText("first ")))
	secondHeld := model.holdFor("second prompt", frames(evText("second ")))
	child, firstSession := startSession(t, withModel(model))
	secondSession := newSession(t, child, child.cwd)

	firstPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: firstSession,
		Prompt:    textPrompt("first prompt"),
	})
	secondPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: secondSession,
		Prompt:    textPrompt("second prompt"),
	})
	firstHeld.await(t)
	secondHeld.await(t)
	firstHeld.finish(sse(evText("answer"), evFinishReason("stop")))
	secondHeld.finish(sse(evText("answer"), evFinishReason("stop")))

	firstResponse := promptResponse(t, child.result(child.await(firstPrompt)))
	firstUpdates := updates(t, child, firstSession)
	secondResponse := promptResponse(t, child.result(child.await(secondPrompt)))
	secondUpdates := updates(t, child, secondSession)
	if firstResponse.StopReason != acp.StopReasonEndTurn ||
		secondResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reasons = %q, %q, want two %q",
			firstResponse.StopReason, secondResponse.StopReason, acp.StopReasonEndTurn)
	}
	firstAnswer, firstMessageID := chunks(
		t, firstUpdates, acp.SessionUpdateAgentMessageChunk,
	)
	secondAnswer, secondMessageID := chunks(
		t, secondUpdates, acp.SessionUpdateAgentMessageChunk,
	)
	if firstAnswer != "first answer" {
		t.Fatalf("first answer = %q, want %q", firstAnswer, "first answer")
	}
	if secondAnswer != "second answer" {
		t.Fatalf("second answer = %q, want %q", secondAnswer, "second answer")
	}
	if firstMessageID == secondMessageID {
		t.Fatalf("sessions share message ID %q", firstMessageID)
	}
}

func TestCancellingOneConcurrentSessionLeavesTheOtherRunning(t *testing.T) {
	model := startModel(t)
	firstHeld := model.holdFor("first prompt", frames(evText("first partial")))
	secondHeld := model.holdFor("second prompt", frames(evText("second partial")))
	model.queueFor("first again", sse(evText("first next"), evFinishReason("stop")))
	model.queueFor("second again", sse(evText("second next"), evFinishReason("stop")))
	child, firstSession := startSession(t, withModel(model))
	secondSession := newSession(t, child, child.cwd)

	firstPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: firstSession,
		Prompt:    textPrompt("first prompt"),
	})
	secondPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: secondSession,
		Prompt:    textPrompt("second prompt"),
	})
	firstHeld.await(t)
	secondHeld.await(t)

	opening := make(map[string][]sessionNotification)
	for range 2 {
		notification := child.notification("session/update")
		var update sessionNotification
		if err := json.Unmarshal(notification.Params, &update); err != nil {
			t.Fatal(err)
		}
		opening[update.SessionID] = append(opening[update.SessionID], update)
	}
	if len(opening[firstSession]) != 1 || len(opening[secondSession]) != 1 {
		t.Fatalf("opening updates by session = %#v, want one each", opening)
	}

	child.notify("session/cancel", acp.CancelNotification{SessionID: firstSession})
	firstResponse := promptResponse(t, child.result(child.await(firstPrompt)))
	if firstResponse.StopReason != acp.StopReasonCancelled {
		t.Fatalf("first stopReason = %q, want %q",
			firstResponse.StopReason, acp.StopReasonCancelled)
	}

	secondHeld.finish(sse(evText(" complete"), evFinishReason("stop")))
	secondResponse := promptResponse(t, child.result(child.await(secondPrompt)))
	if secondResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("second stopReason = %q, want %q",
			secondResponse.StopReason, acp.StopReasonEndTurn)
	}
	secondUpdates := append(opening[secondSession], updates(t, child, secondSession)...)
	secondAnswer, _ := chunks(t, secondUpdates, acp.SessionUpdateAgentMessageChunk)
	if secondAnswer != "second partial complete" {
		t.Fatalf("second answer = %q, want %q", secondAnswer, "second partial complete")
	}

	child.request("session/prompt", acp.PromptRequest{
		SessionID: firstSession,
		Prompt:    textPrompt("first again"),
	})
	updates(t, child, firstSession)
	child.request("session/prompt", acp.PromptRequest{
		SessionID: secondSession,
		Prompt:    textPrompt("second again"),
	})
	updates(t, child, secondSession)

	assertConversation(t, model.requestFor("first again").Messages, []exchange{
		{role: "user", text: "first prompt"},
		{role: "assistant", text: "first partial"},
		{role: "user", text: "first again"},
	})
	assertConversation(t, model.requestFor("second again").Messages, []exchange{
		{role: "user", text: "second prompt"},
		{role: "assistant", text: "second partial complete"},
		{role: "user", text: "second again"},
	})
}

func TestCancellingASessionBeforeItsFirstDeltaLeavesAnotherStreaming(t *testing.T) {
	model := startModel(t)
	silent := model.holdFor("silent prompt", "")
	streaming := model.holdFor("streaming prompt", frames(evText("streaming ")))
	child, silentSession := startSession(t, withModel(model))
	streamingSession := newSession(t, child, child.cwd)

	silentPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: silentSession,
		Prompt:    textPrompt("silent prompt"),
	})
	streamingPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: streamingSession,
		Prompt:    textPrompt("streaming prompt"),
	})
	silent.await(t)
	streaming.await(t)

	child.notify("session/cancel", acp.CancelNotification{SessionID: silentSession})
	silentResponse := promptResponse(t, child.result(child.await(silentPrompt)))
	if silentResponse.StopReason != acp.StopReasonCancelled {
		t.Fatalf("silent stopReason = %q, want %q",
			silentResponse.StopReason, acp.StopReasonCancelled)
	}
	// The streaming session's chunk may already be buffered, and draining the
	// cancelled session must leave it there for its own caller.
	if sent := updates(t, child, silentSession); len(sent) != 0 {
		t.Fatalf("cancelled session sent %d updates, want none: %#v", len(sent), sent)
	}

	streaming.finish(sse(evText("answer"), evFinishReason("stop")))
	streamingResponse := promptResponse(t, child.result(child.await(streamingPrompt)))
	if streamingResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("streaming stopReason = %q, want %q",
			streamingResponse.StopReason, acp.StopReasonEndTurn)
	}
	answer, _ := chunks(
		t, updates(t, child, streamingSession), acp.SessionUpdateAgentMessageChunk,
	)
	if answer != "streaming answer" {
		t.Fatalf("streaming answer = %q, want %q", answer, "streaming answer")
	}
}

func TestARefusedPromptLeavesAnotherSessionsTurnRunning(t *testing.T) {
	model := startModel(t)
	held := model.holdFor("running prompt", frames(evText("running ")))
	child, runningSession := startSession(t, withModel(model))
	refusedSession := newSession(t, child, child.cwd)

	runningPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: runningSession,
		Prompt:    textPrompt("running prompt"),
	})
	held.await(t)

	responseError := child.requestError("session/prompt", acp.PromptRequest{
		SessionID: refusedSession,
		Prompt: []acp.ContentBlock{{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file:///tmp/blob", Blob: e2ePointer("YmxvYg=="),
		}}},
	})
	if responseError.Code != -32602 {
		t.Fatalf("error code = %d (%s), want -32602",
			responseError.Code, responseError.Message)
	}

	held.finish(sse(evText("answer"), evFinishReason("stop")))
	response := promptResponse(t, child.result(child.await(runningPrompt)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("running stopReason = %q, want %q", response.StopReason, acp.StopReasonEndTurn)
	}
	answer, _ := chunks(
		t, updates(t, child, runningSession), acp.SessionUpdateAgentMessageChunk,
	)
	if answer != "running answer" {
		t.Fatalf("running answer = %q, want %q", answer, "running answer")
	}
	if requests := model.requests(); len(requests) != 1 {
		t.Fatalf("model received %d requests, want 1", len(requests))
	}
}

func TestConcurrentSessionHistoriesStaySeparate(t *testing.T) {
	model := startModel(t)
	model.queueFor("first one", sse(evText("first answer"), evFinishReason("stop")))
	model.queueFor("second one", sse(evText("second answer"), evFinishReason("stop")))
	model.queueFor("first two", sse(evText("first done"), evFinishReason("stop")))
	model.queueFor("second two", sse(evText("second done"), evFinishReason("stop")))
	child, firstSession := startSession(t, withModel(model))
	secondSession := newSession(t, child, child.cwd)

	firstPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: firstSession,
		Prompt:    textPrompt("first one"),
	})
	secondPrompt := child.begin("session/prompt", acp.PromptRequest{
		SessionID: secondSession,
		Prompt:    textPrompt("second one"),
	})
	child.result(child.await(firstPrompt))
	updates(t, child, firstSession)
	child.result(child.await(secondPrompt))
	updates(t, child, secondSession)

	firstPrompt = child.begin("session/prompt", acp.PromptRequest{
		SessionID: firstSession,
		Prompt:    textPrompt("first two"),
	})
	secondPrompt = child.begin("session/prompt", acp.PromptRequest{
		SessionID: secondSession,
		Prompt:    textPrompt("second two"),
	})
	child.result(child.await(secondPrompt))
	updates(t, child, secondSession)
	child.result(child.await(firstPrompt))
	updates(t, child, firstSession)

	assertConversation(t, model.requestFor("first two").Messages, []exchange{
		{role: "user", text: "first one"},
		{role: "assistant", text: "first answer"},
		{role: "user", text: "first two"},
	})
	assertConversation(t, model.requestFor("second two").Messages, []exchange{
		{role: "user", text: "second one"},
		{role: "assistant", text: "second answer"},
		{role: "user", text: "second two"},
	})
}

func TestServerStaysResponsiveWithManyConcurrentTurns(t *testing.T) {
	turnCount := max(32, runtime.NumCPU()+8)
	model := startModel(t)
	child, firstSession := startSession(t, withModel(model))

	sessions := make([]string, turnCount)
	sessions[0] = firstSession
	held := make([]*modelResponse, turnCount)
	prompts := make([]call, turnCount)
	for index := range turnCount {
		if index != 0 {
			sessions[index] = newSession(t, child, child.cwd)
		}
		text := "held " + strconv.Itoa(index)
		held[index] = model.holdFor(text, "")
		prompts[index] = child.begin("session/prompt", acp.PromptRequest{
			SessionID: sessions[index],
			Prompt:    textPrompt(text),
		})
	}
	for _, response := range held {
		response.await(t)
	}

	freshSession := newSession(t, child, child.cwd)
	model.queueFor("fresh prompt", sse(evText("fresh answer"), evFinishReason("stop")))
	freshResponse := promptResponse(t, child.request("session/prompt", acp.PromptRequest{
		SessionID: freshSession,
		Prompt:    textPrompt("fresh prompt"),
	}))
	if freshResponse.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("fresh stopReason = %q, want %q",
			freshResponse.StopReason, acp.StopReasonEndTurn)
	}
	updates(t, child, freshSession)

	child.notify("session/cancel", acp.CancelNotification{SessionID: sessions[0]})
	cancelled := promptResponse(t, child.result(child.await(prompts[0])))
	if cancelled.StopReason != acp.StopReasonCancelled {
		t.Fatalf("cancelled stopReason = %q, want %q",
			cancelled.StopReason, acp.StopReasonCancelled)
	}

	for index := 1; index < turnCount; index++ {
		held[index].finish(sse(evFinishReason("stop")))
	}
	for index := 1; index < turnCount; index++ {
		response := promptResponse(t, child.result(child.await(prompts[index])))
		if response.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("turn %d stopReason = %q, want %q",
				index, response.StopReason, acp.StopReasonEndTurn)
		}
		updates(t, child, sessions[index])
	}
}
