package agent

import (
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

func TestPromptMessageRendersEveryBlockAsText(t *testing.T) {
	message := promptMessage([]acp.ContentBlock{
		{Type: "text", Text: "look at"},
		{Type: "resource_link", Name: "main.go", URI: "file:///workspace/main.go"},
		{Type: "resource_link", Name: `a [b] c\d`, URI: "file:///a(b)c"},
	})

	if message.Role != openrouter.RoleUser {
		t.Fatalf("role = %q, want %q", message.Role, openrouter.RoleUser)
	}
	want := []openrouter.ContentBlock{
		{Type: "text", Text: "look at"},
		{Type: "text", Text: "[main.go](file:///workspace/main.go)"},
		{Type: "text", Text: `[a \[b\] c\\d](file:///a(b\)c)`},
	}
	if len(message.Content) != len(want) {
		t.Fatalf("content = %#v", message.Content)
	}
	for index := range want {
		if message.Content[index] != want[index] {
			t.Fatalf("block %d = %#v, want %#v", index, message.Content[index], want[index])
		}
	}
}

func TestPromptMessagePanicsOnUnvalidatedContent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("promptMessage accepted an unsupported content type")
		}
	}()
	promptMessage([]acp.ContentBlock{{Type: "image"}})
}

func TestStopReason(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   acp.StopReason
	}{
		{reason: "stop", want: acp.StopReasonEndTurn},
		{reason: "", want: acp.StopReasonEndTurn},
		{reason: "tool_calls", want: acp.StopReasonEndTurn},
		{reason: "length", want: acp.StopReasonMaxTokens},
		{reason: "content_filter", want: acp.StopReasonRefusal},
		{reason: "refusal", want: acp.StopReasonRefusal},
	} {
		t.Run(test.reason, func(t *testing.T) {
			if got := stopReason(test.reason); got != test.want {
				t.Fatalf("stopReason(%q) = %q, want %q", test.reason, got, test.want)
			}
		})
	}
}
