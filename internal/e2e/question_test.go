package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func formCapabilities() *acp.ClientCapabilities {
	return &acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{
		Form: &acp.ElicitationFormCapabilities{},
	}}
}

func TestFormQuestionRoundTrip(t *testing.T) {
	model := startModel(t,
		toolResponse(
			"question-call", "question",
			`{"question":"Choose.","options":[{"label":"Safe","description":"Small changes."},{"label":"Fast"}],"default":"Safe"}`,
		),
		sse(evText("done"), evFinishReason("stop")),
	)
	child := start(t, withModel(model))
	initializeWithCapabilities(t, child, formCapabilities())
	defer child.stop()
	session := newSession(t, child, child.cwd)
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("ask me"),
	})

	callback := child.serverRequest()
	if callback.Method != acp.MethodElicitationCreate {
		t.Fatalf("callback method = %q", callback.Method)
	}
	var request acp.CreateElicitationRequest
	if err := json.Unmarshal(callback.Params, &request); err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	property := request.RequestedSchema.Properties["answer"]
	if request.SessionID != session || request.ToolCallID == "" || request.Message != "Choose." ||
		len(property.OneOf) != 2 || property.OneOf[0].Const != "Safe" ||
		property.OneOf[0].Description != "Small changes." || property.Default == nil ||
		*property.Default != "Safe" {
		t.Fatalf("request = %#v", request)
	}
	child.respond(callback, acp.CreateElicitationResponse{
		Action:  acp.ElicitationActionAccept,
		Content: map[string]json.RawMessage{"answer": json.RawMessage(`"Safe"`)},
	})
	response := promptResponse(t, child.result(child.await(turn)))
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)

	requests := model.requests()
	if len(requests) != 2 || !requestContainsTool(requests[0], "question") ||
		!requestContainsText(requests[1], `{"outcome":"accepted","answer":"Safe"}`) {
		t.Fatalf("model requests = %#v", requests)
	}
}

func TestFormQuestionIsAbsentWithoutCapability(t *testing.T) {
	model := startModel(t, sse(evText("done"), evFinishReason("stop")))
	child, session := startSession(t, withModel(model))
	defer child.stop()
	prompt(t, child, session, "answer normally")
	_ = updates(t, child, session)
	requests := model.requests()
	if len(requests) != 1 || requestContainsTool(requests[0], "question") {
		t.Fatalf("question availability = %#v", requests)
	}
}

func TestFormQuestionDistinctClientOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		response   *acp.CreateElicitationResponse
		errorReply bool
		want       string
	}{
		{name: "declined", response: &acp.CreateElicitationResponse{Action: acp.ElicitationActionDecline}, want: `{"outcome":"declined"}`},
		{name: "cancelled", response: &acp.CreateElicitationResponse{Action: acp.ElicitationActionCancel}, want: `{"outcome":"cancelled"}`},
		{name: "invalid", response: &acp.CreateElicitationResponse{Action: acp.ElicitationActionAccept}, want: "accepted elicitation content"},
		{name: "callback error", errorReply: true, want: "client refused"},
	}
	responses := make([]string, 0, len(tests)*2)
	for index := range tests {
		responses = append(responses,
			toolResponse(fmt.Sprintf("question-call-%d", index), "question", `{"question":"Continue?"}`),
			sse(evText("done"), evFinishReason("stop")),
		)
	}
	model := startModel(t, responses...)
	child := start(t, withModel(model))
	initializeWithCapabilities(t, child, formCapabilities())
	defer child.stop()
	session := newSession(t, child, child.cwd)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turn := child.begin("session/prompt", acp.PromptRequest{
				SessionID: session, Prompt: textPrompt(test.name),
			})
			callback := child.serverRequest()
			if callback.Method != acp.MethodElicitationCreate {
				t.Fatalf("callback method = %q", callback.Method)
			}
			if test.errorReply {
				child.respondError(callback, -32603, "client refused")
			} else {
				child.respond(callback, test.response)
			}
			response := promptResponse(t, child.result(child.await(turn)))
			if response.StopReason != acp.StopReasonEndTurn {
				t.Fatalf("stop reason = %q", response.StopReason)
			}
			_ = updates(t, child, session)
			requests := model.requests()
			if !requestContainsText(requests[len(requests)-1], test.want) {
				t.Fatalf("continuation omitted %q: %#v", test.want, requests[len(requests)-1])
			}
		})
	}
}

func TestUnansweredFormQuestionIsInterruptedOnRestart(t *testing.T) {
	dataDir := t.TempDir()
	model := startModel(t, toolResponse(
		"question-call", "question", `{"question":"Still there?"}`,
	))
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first := start(t, options...)
	initializeWithCapabilities(t, first, formCapabilities())
	session := newSession(t, first, first.cwd)
	_ = first.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("ask and wait"),
	})
	callback := first.serverRequest()
	if callback.Method != acp.MethodElicitationCreate {
		t.Fatalf("callback method = %q", callback.Method)
	}
	cwd := first.cwd
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	loadSession(t, second, session, cwd)
	sent := updates(t, second, session)
	second.stop()

	interrupted := false
	for _, update := range sent {
		if update.Update.SessionUpdate == "tool_call_update" &&
			strings.Contains(update.Update.Content.Text, "question interrupted") {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("replay updates = %#v", sent)
	}
	if requests := model.requests(); len(requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(requests))
	}
}
