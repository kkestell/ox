package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	fake "ox/internal/openroutertest"
	"ox/internal/settings"
	"ox/internal/transcript"
)

func TestInboxAddsMessagesAtTurnStart(t *testing.T) {
	f := newFixture(t, fake.Text("Done."))
	f.agent.Hooks.UserPromptSubmit = []settings.Hook{{Command: hookReply("Facts"), Timeout: 10 * time.Second}}
	messages := []string{"Message from one:\nFirst", "Message from two:\nSecond"}
	in := f.input("Hello")
	in.Inbox = func() []string {
		waiting := messages
		messages = nil
		return waiting
	}
	client := &recorder{mode: transcript.ModeAuto}
	result, err := f.run(t, context.Background(), in, client)
	if err != nil || result.Answer != "Done." {
		t.Fatalf("result %+v, %v", result, err)
	}
	want := []string{"Context added by the user's hooks:\n\nFacts", "Message from one:\nFirst", "Message from two:\nSecond"}
	if got := f.saved(t)[0].(*transcript.TurnStart).Context; !reflect.DeepEqual(got, want) {
		t.Errorf("saved context = %q", got)
	}
	text := strings.Join(want, "\n\n")
	if next := messageAfter(t, f.server.Bodies()[0], "Hello"); next["role"] != "user" || next["content"] != text {
		t.Errorf("the message after the prompt is %v", next)
	}
	if sent, ok := client.events[1].(Context); !ok || string(sent) != text {
		t.Errorf("the client was sent %v after the session info", client.events[1])
	}
}

func TestInboxAddsMessagesAfterEachToolCall(t *testing.T) {
	for _, mode := range []transcript.Mode{transcript.ModeAuto, transcript.ModeAsk} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t, fake.Shell("printf one", "printf two"), fake.Text("Done."))
			client := &recorder{mode: mode, answers: []bool{false, true}}
			messages := []string{"Message from one:\nFirst", "Message from two:\nSecond"}
			calls := 0
			in := f.input("Run it")
			in.Inbox = func() []string {
				calls++
				if calls == 2 || calls == 3 {
					return messages[calls-2 : calls-1]
				}
				return nil
			}
			result, err := f.run(t, context.Background(), in, client)
			if err != nil || result.Answer != "Done." {
				t.Fatalf("result %+v, %v", result, err)
			}
			if calls != 4 {
				t.Errorf("inbox called %d times, want turn start, two tool calls, and stop", calls)
			}
			if got := f.saved(t)[1].(*transcript.AssistantBatch).Context; !reflect.DeepEqual(got, messages) {
				t.Errorf("saved context = %q", got)
			}
			var sent []string
			for _, event := range client.events {
				switch event := event.(type) {
				case ToolFinished:
					sent = append(sent, event.Call.CallID)
				case Context:
					sent = append(sent, string(event))
				}
			}
			if want := []string{"shell-0", messages[0], "shell-1", messages[1]}; !reflect.DeepEqual(sent, want) {
				t.Errorf("tool outcomes and messages sent = %q", sent)
			}
			bodies := f.server.Bodies()
			requestMessages := bodies[1]["messages"].([]any)
			if last := requestMessages[len(requestMessages)-1].(map[string]any); last["role"] != "user" || last["content"] != strings.Join(messages, "\n\n") {
				t.Errorf("the second request ends with %v", last)
			}
		})
	}
}

func TestInboxMessagesAtStopContinueTheTurn(t *testing.T) {
	f := newFixture(t, fake.Text("First."), fake.Text("Second."))
	f.agent.Hooks.Stop = []settings.Hook{{Command: "cat >> stop.json; echo >> stop.json", Timeout: 10 * time.Second}}
	messages := []string{"Message from one:\nContinue", "Message from two:\nMore work"}
	calls := 0
	in := f.input("Hello")
	in.Inbox = func() []string {
		calls++
		if calls == 2 {
			return messages
		}
		return nil
	}
	client := &recorder{mode: transcript.ModeAuto}
	result, err := f.run(t, context.Background(), in, client)
	if err != nil || result.Stop != EndTurn || result.Answer != "Second." {
		t.Fatalf("result %+v, %v", result, err)
	}
	if calls != 3 {
		t.Errorf("inbox called %d times, want turn start and two stops", calls)
	}
	if got := f.saved(t)[1].(*transcript.AssistantBatch).Context; !reflect.DeepEqual(got, messages) {
		t.Errorf("saved context = %q", got)
	}
	bodies := f.server.Bodies()
	if len(bodies) != 2 {
		t.Fatalf("%d model requests, want 2", len(bodies))
	}
	requestMessages := bodies[1]["messages"].([]any)
	if last := requestMessages[len(requestMessages)-1].(map[string]any); last["role"] != "user" || last["content"] != strings.Join(messages, "\n\n") {
		t.Errorf("the second request ends with %v", last)
	}
	var sent []Context
	for _, event := range client.events {
		if event, ok := event.(Context); ok {
			sent = append(sent, event)
		}
	}
	if len(sent) != 1 || string(sent[0]) != strings.Join(messages, "\n\n") {
		t.Errorf("context sent = %q", sent)
	}
	data, err := os.ReadFile(filepath.Join(f.workspace, "stop.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"stop_hook_active":false`) != 2 || strings.Contains(string(data), `"stop_hook_active":true`) {
		t.Errorf("inbox messages changed the Stop hook state: %s", data)
	}
}

func TestCancelledTurnLeavesInboxUntouched(t *testing.T) {
	f := newFixture(t)
	in := f.input("Hello")
	in.Inbox = func() []string {
		t.Fatal("a cancelled turn took messages from the inbox")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := f.run(t, ctx, in, &recorder{mode: transcript.ModeAuto})
	if err != nil || result.Stop != Cancelled {
		t.Fatalf("result %+v, %v", result, err)
	}
	if got := f.saved(t); len(got) != 0 {
		t.Errorf("saved entries = %v", got)
	}
}
