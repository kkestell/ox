package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
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

func promptResponse(t *testing.T, result json.RawMessage) acp.PromptResponse {
	t.Helper()
	var response acp.PromptResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("decode session/prompt result: %v", err)
	}
	return response
}

// updates decodes the session/update notifications ox sent before the response
// the caller has already read.
func updates(t *testing.T, child *process) []acp.SessionNotification {
	t.Helper()
	var decoded []acp.SessionNotification
	for _, notification := range child.notifications("session/update") {
		var update acp.SessionNotification
		if err := json.Unmarshal(notification.Params, &update); err != nil {
			t.Fatalf("decode session/update params: %v", err)
		}
		decoded = append(decoded, update)
	}
	return decoded
}

// chunks concatenates the text of the updates of one kind and returns the
// message ID they all carry.
func chunks(
	t *testing.T,
	sent []acp.SessionNotification,
	kind, session string,
) (string, string) {
	t.Helper()
	var text strings.Builder
	var messageID string
	seen := 0
	for _, update := range sent {
		if update.Update.SessionUpdate != kind {
			continue
		}
		if update.SessionID != session {
			t.Fatalf("%s session = %q, want %q", kind, update.SessionID, session)
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

func assertContent(t *testing.T, got []modelContentPart, want string) {
	t.Helper()
	var decoded []modelContentPart
	if err := json.Unmarshal([]byte(want), &decoded); err != nil {
		t.Fatalf("decode expected model content: %v", err)
	}
	if !reflect.DeepEqual(got, decoded) {
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("model content mismatch\ngot:  %s\nwant: %s", gotJSON, want)
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

	sent := updates(t, child)
	if len(sent) != 2 {
		t.Fatalf("agent sent %d updates, want 2: %#v", len(sent), sent)
	}
	answer, _ := chunks(t, sent, acp.SessionUpdateAgentMessageChunk, session)
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

	sent := updates(t, child)
	if len(sent) != 3 {
		t.Fatalf("agent sent %d updates, want 3: %#v", len(sent), sent)
	}
	if sent[0].Update.SessionUpdate != acp.SessionUpdateAgentThoughtChunk {
		t.Errorf("first update = %q, want %q",
			sent[0].Update.SessionUpdate, acp.SessionUpdateAgentThoughtChunk)
	}
	thought, thoughtID := chunks(t, sent, acp.SessionUpdateAgentThoughtChunk, session)
	answer, messageID := chunks(t, sent, acp.SessionUpdateAgentMessageChunk, session)
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
		updates(t, child)
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
	updates(t, child)

	assertConversation(t, model.requests()[0].Messages, []exchange{
		{role: "user", text: "look at [main.go](file:///workspace/main.go)"},
	})
}

func TestPromptSendsEveryContentVariantInClientOrder(t *testing.T) {
	model := startModel(t, sse(evText("done"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))

	child.request("session/prompt", acp.PromptRequest{
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
	updates(t, child)

	requests := model.requests()
	if len(requests) != 1 || len(requests[0].Messages) != 1 {
		t.Fatalf("model requests = %#v, want one request with one message", requests)
	}
	want := `[
		{"type":"text","text":"inspect these"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,cGljdHVyZQ=="}},
		{"type":"input_audio","input_audio":{"data":"c291bmQ=","format":"wav"}},
		{"type":"text","text":"[a \\[file\\]](file:///tmp/a(b\\).go)"},
		{"type":"text","text":"[/tmp/context file.txt:12-14]\n` + "````" + `\ninside ` + "```" + ` a fence\n` + "````" + `"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}},
		{"type":"input_audio","input_audio":{"data":"YXVkaW8=","format":"flac"}}
	]`
	assertContent(t, requests[0].Messages[0].Content, want)
}

func TestPromptKeepsMultimodalPartsInHistory(t *testing.T) {
	model := startModel(t,
		sse(evText("first answer"), evFinishReason("stop")),
		sse(evText("second answer"), evFinishReason("stop")),
	)
	child, session := startSession(t, withModel(model))

	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt: []acp.ContentBlock{
			{Type: "text", Text: "describe"},
			{Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ=="},
		},
	})
	updates(t, child)
	child.request("session/prompt", acp.PromptRequest{
		SessionID: session,
		Prompt:    textPrompt("again"),
	})
	updates(t, child)

	requests := model.requests()
	if len(requests) != 2 || len(requests[1].Messages) != 3 {
		t.Fatalf("second model request = %#v, want three messages", requests)
	}
	assertContent(t, requests[1].Messages[0].Content, `[
		{"type":"text","text":"describe"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,cGljdHVyZQ=="}}
	]`)
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
			updates(t, child)
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
			updates(t, child)
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
			updates(t, child)
			if response.StopReason != acp.StopReasonRefusal {
				t.Fatalf("stopReason = %q, want %q", response.StopReason, acp.StopReasonRefusal)
			}

			child.request("session/prompt", acp.PromptRequest{
				SessionID: session,
				Prompt:    textPrompt("next prompt"),
			})
			updates(t, child)

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
	updates(t, child)
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
			name:  "error chunk",
			queue: func(model *mockModel) { model.queue(sse(evError(502, "provider unavailable"))) },
			want:  "provider unavailable",
		},
		{
			name: "non-2xx response",
			queue: func(model *mockModel) {
				model.fail(http.StatusInternalServerError, "provider exploded")
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
	updates(t, child)
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
	var streamed acp.SessionNotification
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
	updates(t, child)
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
	if sent := updates(t, child); len(sent) != 0 {
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
