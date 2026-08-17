package agent

import (
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

func TestPromptMessageTranslatesEveryContentVariant(t *testing.T) {
	message, err := promptMessage([]acp.ContentBlock{
		{Type: "text", Text: "look at"},
		{Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ=="},
		{Type: "audio", MIMEType: "audio/x-wav", Data: "c291bmQ="},
		{Type: "resource_link", Name: "main.go", URI: "file:///workspace/main.go"},
		{Type: "resource_link", Name: `a [b] c\d`, URI: "file:///a(b)c"},
		{Type: "resource", Resource: &acp.EmbeddedResource{
			URI:  "file:///tmp/context%20file.txt#L12-L14",
			Text: pointer("inside ``` a fence"),
		}},
		{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file:///tmp/picture.png", MIMEType: "image/png", Blob: pointer("YmxvYg=="),
		}},
		{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file:///tmp/sound.mp3", MIMEType: "audio/mpeg", Blob: pointer("YmxvYg=="),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if message.Role != openrouter.RoleUser {
		t.Fatalf("role = %q, want %q", message.Role, openrouter.RoleUser)
	}
	want := []openrouter.ContentBlock{
		{Type: "text", Text: "look at"},
		{Type: "image_url", ImageURL: "data:image/png;base64,cGljdHVyZQ=="},
		{Type: "input_audio", AudioData: "c291bmQ=", AudioFormat: "wav"},
		{Type: "text", Text: "[main.go](file:///workspace/main.go)"},
		{Type: "text", Text: `[a \[b\] c\\d](file:///a(b\)c)`},
		{Type: "text", Text: "[/tmp/context file.txt:12-14]\n````\ninside ``` a fence\n````"},
		{Type: "image_url", ImageURL: "data:image/png;base64,YmxvYg=="},
		{Type: "input_audio", AudioData: "YmxvYg==", AudioFormat: "mp3"},
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

// MIME types are case insensitive and may carry parameters, so neither can
// decide whether a payload reaches the model or what it becomes there.
func TestPromptMessageNormalizesMIMETypes(t *testing.T) {
	message, err := promptMessage([]acp.ContentBlock{
		{Type: "image", MIMEType: "IMAGE/PNG", Data: "cGljdHVyZQ=="},
		{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file:///tmp/picture.png", MIMEType: "Image/PNG; charset=binary",
			Blob: pointer("YmxvYg=="),
		}},
		{Type: "resource", Resource: &acp.EmbeddedResource{
			URI: "file:///tmp/sound.wav", MIMEType: "Audio/X-WAV", Blob: pointer("c291bmQ="),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []openrouter.ContentBlock{
		{Type: "image_url", ImageURL: "data:image/png;base64,cGljdHVyZQ=="},
		{Type: "image_url", ImageURL: "data:image/png;base64,YmxvYg=="},
		{Type: "input_audio", AudioData: "c291bmQ=", AudioFormat: "wav"},
	}
	for index := range want {
		if message.Content[index] != want[index] {
			t.Fatalf("block %d = %#v, want %#v", index, message.Content[index], want[index])
		}
	}
}

func TestPromptMessageRejectsUnroutableContent(t *testing.T) {
	for _, test := range []struct {
		name  string
		block acp.ContentBlock
		want  string
	}{
		{
			name: "blob without mime type",
			block: acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///tmp/blob", Blob: pointer("YmxvYg=="),
			}},
			want: "no MIME type",
		},
		{
			name: "unsupported blob mime type",
			block: acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///tmp/file.pdf", MIMEType: "application/pdf", Blob: pointer("YmxvYg=="),
			}},
			want: "application/pdf",
		},
		{
			name:  "unknown content type",
			block: acp.ContentBlock{Type: "future"},
			want:  `unsupported type "future"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := promptMessage([]acp.ContentBlock{{Type: "text"}, test.block})
			if err == nil || !strings.Contains(err.Error(), "block 2") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("promptMessage() error = %v, want block 2 and %q", err, test.want)
			}
		})
	}
}

func TestAudioFormat(t *testing.T) {
	for _, test := range []struct {
		mimeType string
		want     string
	}{
		{mimeType: "audio/wav", want: "wav"},
		{mimeType: "audio/x-wav", want: "wav"},
		{mimeType: "audio/wave", want: "wav"},
		{mimeType: "audio/mpeg", want: "mp3"},
		{mimeType: "audio/mp3", want: "mp3"},
		{mimeType: "audio/flac", want: "flac"},
		{mimeType: "audio/x-custom", want: "custom"},
		{mimeType: "AUDIO/WAV", want: "wav"},
		{mimeType: "audio/wav; codecs=1", want: "wav"},
	} {
		t.Run(test.mimeType, func(t *testing.T) {
			if got := audioFormat(test.mimeType); got != test.want {
				t.Fatalf("audioFormat(%q) = %q, want %q", test.mimeType, got, test.want)
			}
		})
	}
}

func TestResourceLabel(t *testing.T) {
	for _, test := range []struct {
		uri  string
		want string
	}{
		{uri: "file:///tmp/context.txt", want: "/tmp/context.txt"},
		{uri: "file:///tmp/context%20file.txt", want: "/tmp/context file.txt"},
		{uri: "file:///tmp/context.txt#L12", want: "/tmp/context.txt:12"},
		{uri: "file:///tmp/context.txt#L12-L14", want: "/tmp/context.txt:12-14"},
		{uri: "file:///tmp/context.txt#section", want: "/tmp/context.txt"},
		{uri: "https://example.com/context.txt#L12", want: "https://example.com/context.txt#L12"},
		{uri: "file:context.txt", want: "file:context.txt"},
	} {
		t.Run(test.uri, func(t *testing.T) {
			if got := resourceLabel(test.uri); got != test.want {
				t.Fatalf("resourceLabel(%q) = %q, want %q", test.uri, got, test.want)
			}
		})
	}
}

func TestFenceIsLongerThanEveryBacktickRun(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{text: "plain", want: "```"},
		{text: "` and ``", want: "```"},
		{text: "```code```", want: "````"},
		{text: "starts ```` and ends `", want: "`````"},
	} {
		if got := fence(test.text); got != test.want {
			t.Errorf("fence(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}

func pointer(value string) *string {
	return &value
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
