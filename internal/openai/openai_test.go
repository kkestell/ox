package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"ox/internal/catalog"
	"ox/internal/provider"
	"ox/internal/transcript"
)

type object = map[string]any

const model = "openai:gpt-test"

// accessToken is an unsigned JWT carrying the ChatGPT account ID.
func accessToken(account string) string {
	claims, _ := json.Marshal(object{"https://api.openai.com/auth": object{"chatgpt_account_id": account}})
	return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// fake is a scripted Codex backend and token endpoint.
type fake struct {
	t        *testing.T
	server   *httptest.Server
	replies  []func(w http.ResponseWriter)
	bodies   []object
	headers  []http.Header
	refresh  []url.Values
	saved    []Credential
	credited Credential
}

func start(t *testing.T, replies ...func(w http.ResponseWriter)) *fake {
	f := &fake{t: t, replies: replies, credited: Credential{Access: accessToken("acct-1"), Refresh: "rt-1", Expires: time.Now().Add(time.Hour), AccountID: "acct-1"}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			r.ParseForm()
			f.refresh = append(f.refresh, r.PostForm)
			json.NewEncoder(w).Encode(object{"access_token": accessToken("acct-2"), "refresh_token": "rt-2", "expires_in": 3600})
			return
		}
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			var body object
			if err := json.Unmarshal(data, &body); err != nil {
				t.Error(err)
			}
			f.bodies = append(f.bodies, body)
		}
		f.headers = append(f.headers, r.Header.Clone())
		if len(f.replies) == 0 {
			http.Error(w, "no scripted reply", http.StatusTeapot)
			return
		}
		reply := f.replies[0]
		f.replies = f.replies[1:]
		reply(w)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fake) client() *Client {
	return &Client{
		http: f.server.Client(), endpoint: f.server.URL, tokenEndpoint: f.server.URL + "/oauth/token",
		load:         func() (Credential, error) { return f.credited, nil },
		save:         func(c Credential) error { f.saved = append(f.saved, c); f.credited = c; return nil },
		StallTimeout: time.Second,
	}
}

// events streams the events as SSE.
func events(events ...object) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			data, _ := json.Marshal(event)
			io.WriteString(w, "event: "+event["type"].(string)+"\ndata: "+string(data)+"\n\n")
		}
	}
}

func status(code int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func testModel() *catalog.Model {
	return &catalog.Model{Provider: catalog.OpenAI, ID: "gpt-test", Efforts: []catalog.Effort{catalog.EffortDefault, catalog.EffortHigh}}
}

func turn(model, text string) *transcript.TurnStart {
	message := transcript.TextMessage(text)
	return &transcript.TurnStart{Model: model, Effort: catalog.EffortDefault, Mode: transcript.ModeAsk, Input: transcript.TurnInput{Message: &message}}
}

// drain reads a stream to its completion.
func drain(t *testing.T, client *Client, r provider.Request) ([]provider.Item, error) {
	t.Helper()
	stream, err := client.Stream(context.Background(), r)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var items []provider.Item
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

func request(entries ...transcript.Entry) provider.Request {
	return provider.Request{Model: testModel(), Effort: catalog.EffortDefault, SystemPrompt: "You are Ox.", Transcript: entries, SessionID: "session-1"}
}

func completed(usage object) object {
	return object{"type": "response.completed", "response": object{"usage": usage}}
}

func TestRequestsSendTheTranscriptAsInputItems(t *testing.T) {
	f := start(t, events(completed(nil)), events(completed(nil)))
	image := transcript.ImageAttachment{Data: "aGVsbG8=", MimeType: "image/png"}
	first := turn(model, "")
	first.Input.Message.Parts = []transcript.UserMessagePart{{Text: "Look"}, {Image: &image}}
	reasoning := json.RawMessage(`{"encrypted_content":"opaque","summary":[],"type":"reasoning"}`)
	shell, _ := json.Marshal(object{"type": "function", "function": object{"name": "shell", "description": "Run.", "parameters": object{"type": "object"}}})
	entries := []transcript.Entry{
		first,
		&transcript.AssistantBatch{
			Message: transcript.AssistantMessage{
				Text: "Checking.", Reasoning: "Need to look.",
				ToolCalls:            []transcript.ToolCall{{CallID: "call-1", Name: "shell", Arguments: `{"command":"ls"}`}},
				ContinuationMetadata: []json.RawMessage{reasoning},
			},
			Outcomes: []transcript.ToolOutcome{transcript.Completed("a.go")},
			Context:  []string{"Context added by the user's hooks:\n\nFormatted."},
		},
		&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "Done."}},
	}
	r := request(entries...)
	r.Effort = catalog.EffortHigh
	r.Tools = []json.RawMessage{shell}
	if _, err := drain(t, f.client(), r); err != nil {
		t.Fatal(err)
	}
	body := f.bodies[0]
	fields := object{
		"model": "gpt-test", "instructions": "You are Ox.", "store": false, "stream": true, "prompt_cache_key": "session-1",
		"tool_choice": "auto", "parallel_tool_calls": true, "include": []any{"reasoning.encrypted_content"},
		"reasoning": object{"effort": "high", "summary": "auto"},
		"tools":     []any{object{"type": "function", "name": "shell", "description": "Run.", "parameters": object{"type": "object"}, "strict": false}},
	}
	for key, want := range fields {
		if !reflect.DeepEqual(body[key], want) {
			t.Errorf("%s = %v", key, body[key])
		}
	}
	hook := "Context added by the user's hooks:\n\nFormatted."
	want := []any{
		object{"type": "message", "role": "user", "content": []any{
			object{"type": "input_text", "text": "Look"},
			object{"type": "input_image", "image_url": "data:image/png;base64,aGVsbG8="},
		}},
		object{"type": "reasoning", "encrypted_content": "opaque", "summary": []any{}},
		object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": "Checking."}}},
		object{"type": "function_call", "call_id": "call-1", "name": "shell", "arguments": `{"command":"ls"}`},
		object{"type": "function_call_output", "call_id": "call-1", "output": "a.go"},
		object{"type": "message", "role": "user", "content": []any{object{"type": "input_text", "text": hook}}},
		object{"type": "message", "role": "assistant", "content": []any{object{"type": "output_text", "text": "Done."}}},
	}
	if !reflect.DeepEqual(body["input"], want) {
		got, _ := json.MarshalIndent(body["input"], "", " ")
		t.Errorf("input = %s", got)
	}
	header := f.headers[0]
	if header.Get("Authorization") != "Bearer "+accessToken("acct-1") || header.Get("chatgpt-account-id") != "acct-1" || header.Get("originator") != "ox" ||
		header.Get("session-id") != "session-1" {
		t.Errorf("headers = %v", header)
	}

	// Another model's reasoning items are left out.
	entries[0] = turn("openrouter:other", "Look")
	if _, err := drain(t, f.client(), request(entries...)); err != nil {
		t.Fatal(err)
	}
	for _, item := range f.bodies[1]["input"].([]any) {
		if item.(object)["type"] == "reasoning" {
			t.Error("another model's reasoning was sent")
		}
	}
	if !reflect.DeepEqual(f.bodies[1]["reasoning"], object{"summary": "auto"}) {
		t.Errorf("default effort reasoning = %v", f.bodies[1]["reasoning"])
	}
}

func TestStreamsAssembleTheCompletion(t *testing.T) {
	f := start(t, events(
		object{"type": "response.created"},
		object{"type": "response.reasoning_summary_part.added", "summary_index": 0},
		object{"type": "response.reasoning_summary_text.delta", "delta": "Plan"},
		object{"type": "response.reasoning_summary_part.added", "summary_index": 1},
		object{"type": "response.reasoning_summary_text.delta", "delta": "Act"},
		object{"type": "response.output_item.done", "item": object{"type": "reasoning", "id": "rs_1", "encrypted_content": "opaque", "summary": []any{}}},
		object{"type": "response.output_text.delta", "delta": "Run"},
		object{"type": "response.output_text.delta", "delta": "ning."},
		object{"type": "response.output_item.done", "item": object{"type": "message", "content": []any{object{"type": "output_text", "text": "Running."}}}},
		object{"type": "response.output_item.done", "item": object{"type": "function_call", "id": "fc_1", "call_id": "call-1", "name": "shell", "arguments": `{"command":"ls"}`}},
		object{"type": "response.output_item.done", "item": object{"type": "function_call", "id": "fc_2", "call_id": "call-2", "name": "shell", "arguments": `{"command":"pwd"}`}},
		completed(object{"input_tokens": 100, "input_tokens_details": object{"cached_tokens": 40}, "output_tokens": 20, "output_tokens_details": object{"reasoning_tokens": 5}}),
	))
	items, err := drain(t, f.client(), request(turn(model, "hi")))
	if err != nil {
		t.Fatal(err)
	}
	var text, reasoning string
	for _, item := range items {
		text += item.Text
		reasoning += item.Reasoning
	}
	if text != "Running." || reasoning != "Plan\n\nAct" {
		t.Errorf("deltas = %q %q", text, reasoning)
	}
	completion := items[len(items)-1].Completion
	want := transcript.AssistantMessage{
		Text: "Running.", Reasoning: "Plan\n\nAct",
		ToolCalls: []transcript.ToolCall{
			{CallID: "call-1", Name: "shell", Arguments: `{"command":"ls"}`},
			{CallID: "call-2", Name: "shell", Arguments: `{"command":"pwd"}`},
		},
		ContinuationMetadata: []json.RawMessage{json.RawMessage(`{"encrypted_content":"opaque","summary":[],"type":"reasoning"}`)},
		Usage:                &transcript.Usage{InputTokens: 100, CachedTokens: 40, OutputTokens: 20, ReasoningTokens: 5},
	}
	if completion.Stop != provider.StopToolCalls || !reflect.DeepEqual(completion.Message, want) {
		t.Errorf("completion = %+v", completion)
	}
}

func TestStreamsReportStopsAndFailures(t *testing.T) {
	stop := func(events_ ...object) (*provider.Completion, error) {
		items, err := drain(t, start(t, events(events_...)).client(), request(turn(model, "hi")))
		if err != nil {
			return nil, err
		}
		return items[len(items)-1].Completion, nil
	}
	text := object{"type": "response.output_text.delta", "delta": "Cut"}
	if c, err := stop(text, completed(nil)); err != nil || c.Stop != provider.StopFinished || c.Message.Usage != nil {
		t.Errorf("finished: %+v %v", c, err)
	}
	incomplete := object{"type": "response.incomplete", "response": object{"incomplete_details": object{"reason": "max_output_tokens"}}}
	if c, err := stop(text, incomplete); err != nil || c.Stop != provider.StopTokenLimit {
		t.Errorf("token limit: %+v %v", c, err)
	}
	refusal := object{"type": "response.output_item.done", "item": object{"type": "message", "content": []any{object{"type": "refusal", "refusal": "No."}}}}
	if c, err := stop(refusal, completed(nil)); err != nil || c.Stop != provider.StopRefused {
		t.Errorf("refusal: %+v %v", c, err)
	}
	call := object{"type": "response.output_item.done", "item": object{"type": "function_call", "call_id": "call-1", "name": "shell", "arguments": `{"comm`}}
	if _, err := stop(call, incomplete); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("incomplete call: %v", err)
	}
	failure := func(code string) object {
		return object{"type": "response.failed", "response": object{"error": object{"code": code, "message": "failed " + code}}}
	}
	if _, err := stop(failure("context_length_exceeded")); !errors.Is(err, provider.ErrContextOverflow) {
		t.Errorf("overflow: %v", err)
	}
	if _, err := stop(failure("server_error")); !provider.IsTemporary(err) {
		t.Errorf("server error: %v", err)
	}
	if _, err := stop(failure("invalid_prompt")); err == nil || provider.IsTemporary(err) || !strings.Contains(err.Error(), "failed invalid_prompt") {
		t.Errorf("invalid prompt: %v", err)
	}
	if _, err := stop(text); err == nil || !strings.Contains(err.Error(), "ended before") {
		t.Errorf("early end: %v", err)
	}

	client := start(t,
		status(429, `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`),
		status(429, `{"error":{"type":"rate_limit","message":"slow down"}}`),
		status(400, `{"error":{"code":"context_length_exceeded","message":"too long"}}`),
		status(401, `{"detail":"Unauthorized"}`),
	).client()
	_, err := drain(t, client, request(turn(model, "hi")))
	if err == nil || provider.IsTemporary(err) || !strings.Contains(err.Error(), "usage limit is reached") {
		t.Errorf("usage limit: %v", err)
	}
	if _, err := drain(t, client, request(turn(model, "hi"))); !provider.IsTemporary(err) {
		t.Errorf("rate limit: %v", err)
	}
	if _, err := drain(t, client, request(turn(model, "hi"))); !errors.Is(err, provider.ErrContextOverflow) {
		t.Errorf("overflow status: %v", err)
	}
	if _, err := drain(t, client, request(turn(model, "hi"))); err == nil || provider.IsTemporary(err) || !strings.Contains(err.Error(), "401") {
		t.Errorf("unauthorized: %v", err)
	}
}

func TestCatalogKeepsListedModelsInOrderWithKnownEfforts(t *testing.T) {
	text, err := os.ReadFile("testdata/models.json")
	if err != nil {
		t.Fatal(err)
	}
	f := start(t, func(w http.ResponseWriter) { w.Write(text) })
	models, err := f.client().FetchCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	all := []catalog.Effort{catalog.EffortDefault, catalog.EffortLow, catalog.EffortMedium, catalog.EffortHigh, catalog.EffortXHigh, catalog.EffortMax}
	want := catalog.Catalog{
		{Provider: catalog.OpenAI, ID: "gpt-6.1-sol", Name: "GPT-6.1-Sol", ContextLimit: 500000, AcceptsImages: true, Efforts: all},
		{Provider: catalog.OpenAI, ID: "gpt-5.6-luna", Name: "GPT-5.6-Luna", ContextLimit: 272000, AcceptsImages: true, Efforts: all},
	}
	if !reflect.DeepEqual(models, want) {
		for _, model := range models {
			t.Errorf("model = %+v", *model)
		}
	}
	if f.headers[0].Get("chatgpt-account-id") != "acct-1" {
		t.Errorf("headers = %v", f.headers[0])
	}
	if _, err := ParseCatalog([]byte(`{"models":[{"slug":"x","display_name":"X","visibility":"hide"}]}`)); err == nil {
		t.Error("a catalog without listed models was accepted")
	}
	if _, err := ParseCatalog([]byte(`{"models":[{"display_name":"X","visibility":"list"}]}`)); err == nil {
		t.Error("a model without a slug was accepted")
	}
}

func TestRequestsRefreshAnExpiredSignInFirst(t *testing.T) {
	f := start(t, events(completed(nil)), events(completed(nil)))
	if _, err := drain(t, f.client(), request(turn(model, "hi"))); err != nil {
		t.Fatal(err)
	}
	if len(f.refresh) != 0 || len(f.saved) != 0 {
		t.Fatal("a current sign-in was refreshed")
	}
	f.credited.Expires = time.Now().Add(30 * time.Second)
	if _, err := drain(t, f.client(), request(turn(model, "hi"))); err != nil {
		t.Fatal(err)
	}
	if len(f.refresh) != 1 || f.refresh[0].Get("grant_type") != "refresh_token" || f.refresh[0].Get("refresh_token") != "rt-1" || f.refresh[0].Get("client_id") != clientID {
		t.Fatalf("refresh = %v", f.refresh)
	}
	if len(f.saved) != 1 || f.saved[0].Refresh != "rt-2" || f.saved[0].AccountID != "acct-2" || time.Until(f.saved[0].Expires) < 59*time.Minute {
		t.Errorf("saved = %+v", f.saved)
	}
	if got := f.headers[1].Get("Authorization"); got != "Bearer "+accessToken("acct-2") {
		t.Errorf("authorization after refresh = %s", got)
	}
}
