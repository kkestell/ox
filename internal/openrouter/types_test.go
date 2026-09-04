package openrouter

import (
	"encoding/json"
	"testing"
)

func TestRequestEncodingCarriesCacheReasoningAndProviderPolicy(t *testing.T) {
	disabled := false
	excluded := true
	request := Request{
		Model:        "author/model",
		SessionID:    "session-1",
		CacheControl: &CacheControl{Type: "ephemeral"},
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: "text",
				Text: "hello",
				CacheControl: &CacheControl{
					Type: "ephemeral",
					TTL:  "1h",
				},
			}},
		}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:       "lookup",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		Reasoning: &Reasoning{Enabled: &disabled, Exclude: &excluded},
	}
	request.Provider = request.Provider.resolved(true)
	raw, err := json.Marshal(streamRequest{Request: request, Stream: true})
	if err != nil {
		t.Fatal(err)
	}

	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := encoded["usage"]; ok {
		t.Fatal("deprecated usage field was encoded")
	}
	if _, ok := encoded["stream_options"]; ok {
		t.Fatal("deprecated stream_options field was encoded")
	}
	messages := encoded["messages"].([]any)
	message := messages[0].(map[string]any)
	if _, ok := message["content"].([]any); !ok {
		t.Fatalf("message content is not an array: %s", raw)
	}
	block := message["content"].([]any)[0].(map[string]any)
	cache := block["cache_control"].(map[string]any)
	if cache["type"] != "ephemeral" || cache["ttl"] != "1h" {
		t.Fatalf("cache control = %#v", cache)
	}
	reasoning := encoded["reasoning"].(map[string]any)
	if reasoning["enabled"] != false || reasoning["exclude"] != true {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	provider := encoded["provider"].(map[string]any)
	if provider["require_parameters"] != true {
		t.Fatalf("provider = %#v", provider)
	}
	if encoded["session_id"] != "session-1" {
		t.Fatalf("session ID missing: %s", raw)
	}
	if _, ok := encoded["parallel_tool_calls"]; ok {
		t.Fatalf("parallel_tool_calls prevents strict routing to tool-capable providers: %s", raw)
	}
	topLevelCache := encoded["cache_control"].(map[string]any)
	if topLevelCache["type"] != "ephemeral" {
		t.Fatalf("top-level cache control = %#v", topLevelCache)
	}
}

func TestRequestEncodingOmitsUnsetFields(t *testing.T) {
	raw, err := json.Marshal(streamRequest{
		Request: Request{
			Model: "author/model",
			Messages: []Message{{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: "text", Text: "hello"}},
			}},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"model":"author/model","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"stream":true}` {
		t.Fatalf("encoded request = %s", raw)
	}
}

func TestReasoningDetailsRemainRawAndOrderedAcrossReplay(t *testing.T) {
	details := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning.summary","summary":"first","index":0}`),
		json.RawMessage(`{"type":"reasoning.encrypted","id":"call_1","data":"opaque"}`),
		json.RawMessage(`{"type":"reasoning.text","text":"third","signature":"signed"}`),
	}
	message := Message{Role: RoleAssistant, ReasoningDetails: details}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var replayed Message
	if err := json.Unmarshal(raw, &replayed); err != nil {
		t.Fatal(err)
	}
	if len(replayed.ReasoningDetails) != len(details) {
		t.Fatalf("reasoning details = %d", len(replayed.ReasoningDetails))
	}
	for index := range details {
		if string(replayed.ReasoningDetails[index]) != string(details[index]) {
			t.Fatalf(
				"detail %d = %s, want %s",
				index,
				replayed.ReasoningDetails[index],
				details[index],
			)
		}
	}
}
