package openrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"ox/internal/catalog"
	"ox/internal/openrouter"
	"ox/internal/openroutertest"
	"ox/internal/transcript"
)

type object = map[string]any

func request(model *catalog.Model, effort catalog.Effort, entries ...transcript.Entry) openrouter.Request {
	return openrouter.Request{Model: model, Effort: effort, SystemPrompt: "You are Ox.", Transcript: entries}
}

func userTurn(text string) *transcript.TurnStart {
	message := transcript.TextMessage(text)
	return &transcript.TurnStart{Model: openroutertest.DefaultModel, Effort: catalog.EffortDefault, Mode: transcript.ModeAsk,
		Input: transcript.TurnInput{Message: &message}}
}

func defaultModel() *catalog.Model {
	return openroutertest.ParsedCatalog().Lookup(openroutertest.DefaultModel)
}

// drain reads a stream to its completion.
func drain(t *testing.T, server *openroutertest.Server) ([]openrouter.Item, error) {
	t.Helper()
	stream, err := server.Client().Stream(context.Background(), request(defaultModel(), catalog.EffortDefault, userTurn("hi")))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var items []openrouter.Item
	for {
		item, err := stream.Next()
		if err != nil {
			return items, err
		}
		items = append(items, item)
		if item.Completion != nil {
			return items, nil
		}
	}
}

func complete(t *testing.T, chunks ...map[string]any) (*openrouter.Completion, error) {
	t.Helper()
	items, err := drain(t, openroutertest.Start(t, openroutertest.Chunks(chunks...)))
	if err != nil {
		return nil, err
	}
	return items[len(items)-1].Completion, nil
}

func TestCatalogKeepsRecentUsableModelsByNameWithKnownEfforts(t *testing.T) {
	models := openroutertest.ParsedCatalog()
	var ids []string
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	want := []string{"deepseek/deepseek-v4.1-flash", "z-ai/glm-5.3-flash", "meta/muse-spark-1.3-contributor", "acme/plain"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
	if models[0].InputPrice != 0.03 || models[0].OutputPrice != 0.6 {
		t.Errorf("prices are USD per million tokens: %v %v", models[0].InputPrice, models[0].OutputPrice)
	}
	wantEfforts := []catalog.Effort{"default", "low", "medium", "high", "xhigh", "max"}
	if !reflect.DeepEqual(models[1].Efforts, wantEfforts) || !models[1].AcceptsImages || models[0].AcceptsImages {
		t.Errorf("glm = %+v", models[1])
	}
	if !reflect.DeepEqual(models[3].Efforts, []catalog.Effort{"default"}) {
		t.Errorf("plain efforts = %v", models[3].Efforts)
	}
	for _, text := range []string{`{"data": []}`, `{"data": [{"id": "a/b"}]}`} {
		if _, err := openrouter.ParseCatalog([]byte(text), openroutertest.Now); err == nil {
			t.Errorf("%s parsed", text)
		}
	}
	unpriced := strings.Replace(openroutertest.Catalog, `"prompt": "0.00000003"`, `"prompt": "free"`, 1)
	_, err := openrouter.ParseCatalog([]byte(unpriced), openroutertest.Now)
	if err == nil || !strings.HasPrefix(err.Error(), `malformed OpenRouter model catalog: price "free" is not a decimal number`) {
		t.Errorf("unpriced: %v", err)
	}
}

func TestRequestsGroupMessagesAndSendContinuationMetadata(t *testing.T) {
	server := openroutertest.Start(t, openroutertest.Text("Both sunny."))
	details := []json.RawMessage{json.RawMessage(`{"data":"opaque","format":"openai-responses-v1","id":"rs_1","index":0,"type":"reasoning.encrypted"}`)}
	raw := ` { "command": "printf Chicago" } `
	model := openroutertest.ParsedCatalog().Lookup("openrouter:z-ai/glm-5.3-flash")
	model.Providers = []string{"deepseek", "deepinfra/turbo"}
	entries := []transcript.Entry{
		userTurn("Weather in Chicago and Denver?"),
		&transcript.AssistantBatch{
			Message: transcript.AssistantMessage{
				Reasoning: "Need both cities.",
				ToolCalls: []transcript.ToolCall{
					{CallID: "call-1", Name: "shell", Arguments: raw},
					{CallID: "call-2", Name: "shell", Arguments: `{"command":"printf Denver"}`},
				},
				ContinuationMetadata: details,
			},
			Outcomes: []transcript.ToolOutcome{transcript.Completed("Sunny."), transcript.Failed("Unavailable.")},
		},
		&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "Four.", Reasoning: "Add them."}},
	}
	stream, err := server.Client().Stream(context.Background(), request(model, catalog.EffortHigh, entries...))
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	body := server.Bodies()[0]
	if body["model"] != "z-ai/glm-5.3-flash" || body["stream"] != true || !reflect.DeepEqual(body["usage"], object{"include": true}) {
		t.Errorf("body = %v", body)
	}
	if !reflect.DeepEqual(body["reasoning"], object{"effort": "high"}) {
		t.Errorf("reasoning = %v", body["reasoning"])
	}
	if !reflect.DeepEqual(body["provider"], object{"order": []any{"deepseek", "deepinfra/turbo"}, "allow_fallbacks": false}) {
		t.Errorf("provider = %v", body["provider"])
	}
	messages := body["messages"].([]any)
	want := []any{
		object{"role": "system", "content": "You are Ox."},
		object{"role": "user", "content": "Weather in Chicago and Denver?"},
		object{"role": "assistant", "content": nil, "tool_calls": []any{
			object{"id": "call-1", "type": "function", "function": object{"name": "shell", "arguments": raw}},
			object{"id": "call-2", "type": "function", "function": object{"name": "shell", "arguments": `{"command":"printf Denver"}`}},
		}, "reasoning_details": []any{object{"data": "opaque", "format": "openai-responses-v1", "id": "rs_1", "index": 0.0, "type": "reasoning.encrypted"}}},
		object{"role": "tool", "tool_call_id": "call-1", "content": "Sunny."},
		object{"role": "tool", "tool_call_id": "call-2", "content": "Unavailable."},
		object{"role": "assistant", "content": "Four.", "reasoning": "Add them."},
	}
	if !reflect.DeepEqual(messages, want) {
		got, _ := json.MarshalIndent(messages, "", " ")
		t.Errorf("messages = %s", got)
	}
}

func TestRequestsSendTheLatestCompactionAndTheUserMessageBeforeIt(t *testing.T) {
	server := openroutertest.Start(t, openroutertest.Text("ok"))
	details := []json.RawMessage{json.RawMessage(`{"data":"opaque","type":"reasoning.encrypted"}`)}
	entries := []transcript.Entry{
		userTurn("Fix the parser."),
		&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "Looking.", ContinuationMetadata: details}},
		&transcript.Compaction{Summary: "An old summary."},
		&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "Still looking."}},
		&transcript.Compaction{Summary: "The parser is fixed."},
		&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "Done."}},
	}
	stream, err := server.Client().Stream(context.Background(), request(defaultModel(), catalog.EffortDefault, entries...))
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	want := []any{
		object{"role": "system", "content": "You are Ox."},
		object{"role": "user", "content": "The earlier part of this session was replaced by this summary:\n\nThe parser is fixed."},
		object{"role": "user", "content": "Fix the parser."},
		object{"role": "assistant", "content": "Done."},
	}
	if messages := server.Bodies()[0]["messages"]; !reflect.DeepEqual(messages, want) {
		got, _ := json.MarshalIndent(messages, "", " ")
		t.Errorf("messages = %s", got)
	}
}

func TestRequestsOmitDefaultEffortAndKeepImageOrder(t *testing.T) {
	server := openroutertest.Start(t, openroutertest.Text("ok"))
	image := transcript.ImageAttachment{Data: "aGVsbG8=", MimeType: "image/png"}
	message := transcript.UserMessage{Parts: []transcript.UserMessagePart{{Text: "Before"}, {Image: &image}, {Text: "After"}}}
	skill := &transcript.SkillInvocation{Name: "goal", Arguments: "Inspect", Instructions: "Look at the screenshot.", Images: []transcript.ImageAttachment{image}}
	stream, err := server.Client().Stream(context.Background(), request(defaultModel(), catalog.EffortDefault,
		&transcript.TurnStart{Input: transcript.TurnInput{Message: &message}},
		&transcript.TurnStart{Input: transcript.TurnInput{Skill: skill}}))
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	body := server.Bodies()[0]
	if _, ok := body["reasoning"]; ok {
		t.Error("default effort sent a reasoning effort")
	}
	if _, ok := body["provider"]; ok {
		t.Error("an unpinned model sent a provider route")
	}
	messages := body["messages"].([]any)
	url := object{"url": "data:image/png;base64,aGVsbG8="}
	if want := []any{object{"type": "text", "text": "Before"}, object{"type": "image_url", "image_url": url}, object{"type": "text", "text": "After"}}; !reflect.DeepEqual(messages[1].(object)["content"], want) {
		t.Errorf("user content = %v", messages[1])
	}
	skillContent := messages[2].(object)["content"].([]any)
	if skillContent[0].(object)["text"] != "Skill /goal invoked.\n\nInstructions:\nLook at the screenshot.\n\nArguments:\nInspect" ||
		!reflect.DeepEqual(skillContent[1].(object)["image_url"], url) {
		t.Errorf("skill content = %v", skillContent)
	}
}

func TestStreamsAssembleTextReasoningAndFragmentedToolCalls(t *testing.T) {
	usage := openroutertest.Usage(12, 34, 0.25)
	usage["usage"].(object)["prompt_tokens_details"] = object{"cached_tokens": 8}
	usage["usage"].(object)["completion_tokens_details"] = object{"reasoning_tokens": 20}
	d := openroutertest.Delta
	items, err := drain(t, openroutertest.Start(t, openroutertest.Chunks(
		d(object{"role": "assistant", "reasoning": "Let me ", "reasoning_details": []any{object{"type": "reasoning.text", "text": "Let me ", "index": 0, "format": "x"}}}, ""),
		d(object{"reasoning": "check.", "reasoning_details": []any{object{"type": "reasoning.text", "text": "check.", "index": 0, "format": "x", "signature": "sig"}}}, ""),
		d(object{"reasoning_details": []any{object{"type": "reasoning.encrypted", "data": "blob", "id": "rs_1", "index": 1}}}, ""),
		d(object{"content": "Checking "}, ""),
		d(object{"content": "now."}, ""),
		d(object{"tool_calls": []any{object{"index": 0, "id": "call-1", "type": "function", "function": object{"name": "shell", "arguments": `{"comm`}}}}, ""),
		d(object{"tool_calls": []any{
			object{"index": 0, "function": object{"arguments": `and":"printf Chicago"}`}},
			object{"index": 1, "id": "call-2", "type": "function", "function": object{"name": "shell", "arguments": ""}},
		}}, ""),
		d(object{"tool_calls": []any{object{"index": 1, "function": object{"arguments": `{"command":"printf Denver"}`}}}}, "tool_calls"),
		usage,
	)))
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	for _, item := range items[:len(items)-1] {
		deltas = append(deltas, item.Reasoning+"|"+item.Text)
	}
	if want := []string{"Let me |", "check.|", "|Checking ", "|now."}; !reflect.DeepEqual(deltas, want) {
		t.Errorf("deltas = %q", deltas)
	}
	completion := items[len(items)-1].Completion
	cost := 0.25
	want := transcript.AssistantMessage{
		Text: "Checking now.", Reasoning: "Let me check.",
		ToolCalls: []transcript.ToolCall{
			{CallID: "call-1", Name: "shell", Arguments: `{"command":"printf Chicago"}`},
			{CallID: "call-2", Name: "shell", Arguments: `{"command":"printf Denver"}`},
		},
		ContinuationMetadata: []json.RawMessage{
			json.RawMessage(`{"format":"x","index":0,"signature":"sig","text":"Let me check.","type":"reasoning.text"}`),
			json.RawMessage(`{"data":"blob","id":"rs_1","index":1,"type":"reasoning.encrypted"}`),
		},
		Usage: &transcript.Usage{InputTokens: 12, CachedTokens: 8, OutputTokens: 34, ReasoningTokens: 20, Cost: &cost},
	}
	if completion.Stop != openrouter.StopToolCalls || !reflect.DeepEqual(completion.Message, want) {
		got, _ := json.MarshalIndent(completion.Message, "", " ")
		t.Errorf("stop %v, message %s", completion.Stop, got)
	}
}

func TestReasoningBlocksSurviveReusedOrMissingIndicesAndKeepDistinctIDs(t *testing.T) {
	for _, field := range []string{"summary", "text"} {
		kind := "reasoning." + field
		for _, index := range []any{0, nil} {
			detail := func(kind, field, value string, extra ...any) object {
				d := object{"type": kind, field: value}
				if index != nil {
					d["index"] = index
				}
				for i := 0; i < len(extra); i += 2 {
					d[extra[i].(string)] = extra[i+1]
				}
				return d
			}
			fragments := []object{
				detail(kind, field, "First "), detail(kind, field, "block."),
				detail("reasoning.encrypted", "data", "opaque-first", "id", "rs_first", "extra", object{"nested": []any{1, 2}}),
				detail(kind, field, "Second "), detail(kind, field, "block."),
				detail("reasoning.encrypted", "data", "opaque-second", "id", "rs_second"),
				detail("reasoning.encrypted", "data", "opaque-third", "id", "rs_third"),
			}
			var chunks []map[string]any
			for _, fragment := range fragments {
				chunks = append(chunks, openroutertest.Delta(object{"reasoning_details": []any{fragment}}, ""))
			}
			chunks = append(chunks, openroutertest.Delta(object{"content": "Done."}, "stop"))
			completion, err := complete(t, chunks...)
			if err != nil {
				t.Fatal(err)
			}
			want := []object{detail(kind, field, "First block."), fragments[2], detail(kind, field, "Second block."), fragments[5], fragments[6]}
			assertDetails(t, completion.Message.ContinuationMetadata, want)
		}
	}
	for _, field := range []string{"id", "index"} {
		first := object{"type": "reasoning.summary", "summary": "First.", field: "a"}
		second := object{"type": "reasoning.summary", "summary": "Second.", field: "b"}
		completion, err := complete(t, openroutertest.Delta(object{"reasoning_details": []any{first}}, ""),
			openroutertest.Delta(object{"reasoning_details": []any{second}}, ""), openroutertest.Delta(object{}, "stop"))
		if err != nil {
			t.Fatal(err)
		}
		assertDetails(t, completion.Message.ContinuationMetadata, []object{first, second})
	}
}

func assertDetails(t *testing.T, got []json.RawMessage, want []object) {
	t.Helper()
	encoded, _ := json.Marshal(want)
	var wantValues, gotValues []any
	json.Unmarshal(encoded, &wantValues)
	gotEncoded, _ := json.Marshal(got)
	json.Unmarshal(gotEncoded, &gotValues)
	if !reflect.DeepEqual(gotValues, wantValues) {
		t.Errorf("details = %s, want %s", gotEncoded, encoded)
	}
}

func TestFinishReasonsAreClassifiedOrRejected(t *testing.T) {
	d := openroutertest.Delta
	call := object{"tool_calls": []any{object{"index": 0, "id": "call-1", "function": object{"name": "shell", "arguments": "{"}}}}
	for _, test := range []struct {
		delta  object
		reason string
		stop   openrouter.Stop
		ok     bool
	}{
		{object{"content": "Done."}, "stop", openrouter.StopFinished, true},
		{object{"content": "Cut"}, "length", openrouter.StopTokenLimit, true},
		{object{}, "content_filter", openrouter.StopRefused, true},
		{object{}, "tool_calls", 0, false},
		{call, "length", 0, false},
		{object{}, "weird", 0, false},
		{object{}, "error", 0, false},
		{object{"tool_calls": []any{object{"index": 0, "id": "call-1", "function": object{"arguments": "{}"}}}}, "tool_calls", 0, false},
		{object{"tool_calls": []any{object{"index": 0, "function": object{"name": "shell", "arguments": "{}"}}}}, "tool_calls", 0, false},
	} {
		completion, err := complete(t, d(test.delta, test.reason))
		if (err == nil) != test.ok || (test.ok && completion.Stop != test.stop) {
			t.Errorf("%v %s: completion %v, error %v", test.delta, test.reason, completion, err)
		}
	}
}

func TestIncompleteOrMalformedStreamsAndFailedStatusesAreErrors(t *testing.T) {
	d := openroutertest.Delta
	if _, err := complete(t, d(object{"content": "Hello"}, "")); err == nil || !strings.Contains(err.Error(), "ended before") {
		t.Errorf("unfinished: %v", err)
	}
	midStream := object{"id": "gen-1", "error": object{"code": "server_error", "message": "Provider disconnected"},
		"choices": []any{object{"index": 0, "delta": object{"content": ""}, "finish_reason": "error"}}}
	if _, err := complete(t, d(object{"content": "Hel"}, ""), midStream); err == nil || !strings.Contains(err.Error(), "Provider disconnected") {
		t.Errorf("mid-stream error: %v", err)
	}
	costless := openroutertest.Usage(12, 34, 0.25)
	delete(costless["usage"].(object), "cost")
	if _, err := complete(t, d(object{"content": "Done."}, "stop"), costless); err == nil {
		t.Error("a usage chunk without cost was accepted")
	}
	if _, err := drain(t, openroutertest.Start(t, openroutertest.Stream("data: not json\n\n"))); err == nil {
		t.Error("a malformed chunk was accepted")
	}
	_, err := drain(t, openroutertest.Start(t, openroutertest.Status(429, `{"error":"slow down"}`)))
	if err == nil || !strings.Contains(err.Error(), "429") || !openrouter.IsTemporary(err) {
		t.Errorf("429: %v", err)
	}
	_, err = drain(t, openroutertest.Start(t, openroutertest.Status(401,
		`{"error":{"message":"Provider returned error","code":401,"metadata":{"provider_name":"Meta"}}}`)))
	if err == nil || openrouter.IsTemporary(err) ||
		!strings.HasPrefix(err.Error(), "Meta, the provider OpenRouter routed this request to, returned 401 Unauthorized") {
		t.Errorf("401: %v", err)
	}
	_, err = drain(t, openroutertest.Start(t, openroutertest.Status(400, `{"error":{"message":"prompt tokens exceed the maximum context length"}}`)))
	if !errors.Is(err, openrouter.ErrContextOverflow) {
		t.Errorf("overflow: %v", err)
	}
}

func TestAStreamWithoutDataStallsAsATemporaryFailure(t *testing.T) {
	server := openroutertest.Start(t, openroutertest.Hang(": OPENROUTER PROCESSING\n\n"))
	client := server.Client()
	client.StallTimeout = 100 * time.Millisecond
	stream, err := client.Stream(context.Background(), request(defaultModel(), catalog.EffortDefault, userTurn("hi")))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Next()
	if err == nil || !openrouter.IsTemporary(err) || !strings.Contains(err.Error(), "no response data") {
		t.Errorf("stall: %v", err)
	}
}

func TestStreamPreservesTransportErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	client := openrouter.NewForEndpoint("test-key", endpoint)
	_, err := client.Stream(context.Background(), request(defaultModel(), catalog.EffortDefault, userTurn("hi")))
	if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "OpenRouter request failed") {
		t.Fatalf("transport error = %v", err)
	}
}

func TestVerifyChecksTheKeyWithoutGenerating(t *testing.T) {
	server := openroutertest.Start(t, openroutertest.Status(200, `{"data":{"label":"ok"}}`), openroutertest.Status(401, `{"error":{"message":"bad key"}}`))
	client := server.Client()
	if err := client.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected the API key") {
		t.Errorf("bad key: %v", err)
	}
	if len(server.Bodies()) != 0 {
		t.Error("verification sent a body")
	}
}

func TestStreamAcceptsSSEFramingAndLargeEvents(t *testing.T) {
	text := strings.Repeat("雪", 30000)
	delta, _ := json.Marshal(openroutertest.Delta(object{"content": text}, "stop"))
	// Multiple data fields, a BOM, CR-only endings, comments, and a final usage
	// event all go through the SSE parser before completion assembly.
	payload := "\xef\xbb\xbf: keepalive\r\rid: 1\rdata: " + string(delta[:1]) + "\rdata: " + string(delta[1:]) + "\r\r"
	usage, _ := json.Marshal(openroutertest.Usage(12, 34, 0.25))
	payload += "data: " + string(usage) + "\r\rdata: [DONE]\r\r"
	items, err := drain(t, openroutertest.Start(t, openroutertest.Stream(payload)))
	if err != nil {
		t.Fatal(err)
	}
	completion := items[len(items)-1].Completion
	if completion.Message.Text != text || completion.Message.Usage == nil || completion.Message.Usage.InputTokens != 12 {
		t.Fatal("SSE framing lost text or final usage")
	}
}
