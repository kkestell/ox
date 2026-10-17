package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"ox/internal/mcpclient"
	"ox/internal/mcptest"
	"ox/internal/settings"
	"ox/internal/transcript"
)

func TestMCPHelper(t *testing.T) { mcptest.Run() }

func TestMCPAdapterArgumentsPermissionsAndErrors(t *testing.T) {
	marker := t.TempDir() + "/calls"
	missing := mcptest.Tool("missing", false)
	missing.Annotations = nil
	definition, _ := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("read", true), mcptest.Tool("write", false), missing}, Marker: marker, Calls: map[string]string{"write": "error"}})
	clients := mcpclient.New(t.TempDir(), map[string]settings.MCPServer{"project": definition})
	defer clients.Close()
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	box := &Toolbox{MCP: catalog}
	for _, name := range []string{"read", "write", "missing"} {
		permission := box.Permission(transcript.ToolCall{Name: "mcp__project__" + name})
		if (permission.Kind == PermissionNone) != (name == "read") {
			t.Errorf("%s: %+v", name, permission)
		}
		if name != "read" && (permission.Server != "project" || permission.Tool != name) {
			t.Fatal(permission)
		}
	}
	for _, args := range []string{"null", "[]", "1", "{} {}", "{bad"} {
		result := box.Execute(context.Background(), transcript.ToolCall{Name: "mcp__project__read", Arguments: args})
		if result.Status != transcript.ToolFailed {
			t.Errorf("accepted %s", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := box.Execute(ctx, transcript.ToolCall{Name: "mcp__project__read", Arguments: `{}`}); got.Status != transcript.ToolCancelled {
		t.Fatal(got)
	}
	data, _ := os.ReadFile(marker)
	if strings.Contains(string(data), "call ") {
		t.Fatal("invalid or cancelled call reached server")
	}
	got := box.Execute(context.Background(), transcript.ToolCall{Name: "mcp__project__read", Arguments: `{"number":9007199254740993}`})
	if got.Status != transcript.ToolCompleted || !strings.Contains(got.Text, "9007199254740993") {
		t.Fatal(got)
	}
	got = box.Execute(context.Background(), transcript.ToolCall{Name: "mcp__project__write", Arguments: `{}`})
	if got.Status != transcript.ToolFailed || !strings.Contains(got.Text, "protocol failure") {
		t.Fatal(got)
	}
	if len(box.Schemas()) != len(Schemas())+3 {
		t.Fatal("missing MCP schemas")
	}
}

func TestMCPResultConversion(t *testing.T) {
	for _, test := range []struct {
		name   string
		result mcpclient.Result
		want   string
		status transcript.ToolStatus
	}{
		{"text", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"one"}`), json.RawMessage(`{"type":"text","text":"two"}`)}}, "one\ntwo", transcript.ToolCompleted},
		{"structured", mcpclient.Result{Structured: json.RawMessage(`{"answer":42}`)}, `{"answer":42}`, transcript.ToolCompleted},
		{"deduplicate", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"{\"answer\":42}"}`)}, Structured: json.RawMessage(`{"answer":42}`)}, `{"answer":42}`, transcript.ToolCompleted},
		{"resource", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"resource","resource":{"uri":"test:x","text":"embedded"}}`)}}, "embedded", transcript.ToolCompleted},
		{"link", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"resource_link","uri":"test:x","name":"x","description":"link"}`)}}, `{"type":"resource_link","uri":"test:x","name":"x","description":"link"}`, transcript.ToolCompleted},
		{"image", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"image","data":"AA=="}`)}}, "[MCP image content omitted.]", transcript.ToolCompleted},
		{"audio", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"audio","data":"AA=="}`)}, IsError: true}, "[MCP audio content omitted.]", transcript.ToolFailed},
		{"binary", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"resource","resource":{"uri":"x","blob":"AA=="}}`)}}, "[Binary or unsupported embedded resource omitted.]", transcript.ToolCompleted},
		{"empty", mcpclient.Result{}, "", transcript.ToolCompleted},
		{"error", mcpclient.Result{Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"failed"}`)}, IsError: true}, "failed", transcript.ToolFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := mcpOutcome(test.result)
			if got.Text != test.want || got.Status != test.status {
				t.Fatalf("got %+v", got)
			}
		})
	}
	for _, isError := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": strings.Repeat("🦊", outputLimit)})
		got := mcpOutcome(mcpclient.Result{Content: []json.RawMessage{raw}, IsError: isError})
		if len(got.Text) > outputLimit || !utf8.ValidString(got.Text) || !strings.HasSuffix(got.Text, "MCP result truncated.") || (got.Status == transcript.ToolFailed) != isError {
			t.Fatal("bad truncation")
		}
	}
}

func TestMCPWireResults(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
		failed          bool
	}{
		{"structured", `{"content":[{"type":"text","text":"{\"answer\":42}"}],"structuredContent":{"answer":42}}`, `{"answer":42}`, false},
		{"large integer", `{"content":[],"structuredContent":{"id":9007199254740993}}`, `{"id":9007199254740993}`, false},
		{"precise decimal", `{"content":[],"structuredContent":{"value":0.1234567890123456789}}`, `{"value":0.1234567890123456789}`, false},
		{"resource", `{"content":[{"type":"resource","resource":{"uri":"test:x","text":"embedded"}},{"type":"resource_link","uri":"test:y","name":"link"}]}`, "embedded", false},
		{"image error", `{"isError":true,"content":[{"type":"image","data":"AA==","mimeType":"image/png"}]}`, "image content omitted", true},
		{"malformed", `{"content":42}`, "unmarshal", true},
		{"empty resource", `{"content":[{"type":"resource","resource":{"uri":"test:x","text":""}}]}`, "", false},
		{"empty binary", `{"content":[{"type":"resource","resource":{"uri":"test:x","blob":""}}]}`, "[Binary or unsupported embedded resource omitted.]", false},
		{"unknown content", `{"content":[{"type":"future","data":"value"}]}`, "[Unsupported MCP content omitted.]", false},
		{"unknown error content", `{"isError":true,"content":[{"type":"future","data":"value"}]}`, "[Unsupported MCP content omitted.]", true},
		{"null result", `null`, "result must be a JSON object", true},
		{"null content", `{"content":[null]}`, "Invalid MCP content", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition, _ := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("result", true)}, Results: map[string]json.RawMessage{"result": json.RawMessage(test.raw)}})
			clients := mcpclient.New(t.TempDir(), map[string]settings.MCPServer{"project": definition})
			defer clients.Close()
			catalog, err := clients.Prepare(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			box := Toolbox{MCP: catalog}
			result := box.Execute(context.Background(), transcript.ToolCall{Name: "mcp__project__result", Arguments: `{}`})
			if (result.Status == transcript.ToolFailed) != test.failed || !strings.Contains(result.Text, test.want) {
				t.Fatalf("result: %+v", result)
			}
			if (test.name == "structured" || test.name == "empty resource") && result.Text != test.want {
				t.Fatal("duplicated structured result")
			}
		})
	}
}
