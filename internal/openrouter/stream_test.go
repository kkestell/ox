package openrouter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssemblerSeparatesTextReasoningAndAssemblesParallelToolCalls(t *testing.T) {
	events := []string{
		`{"id":"generation","model":"author/model","provider":"Example","choices":[{"delta":{"reasoning":"think ","tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"second","arguments":"{\"b\":"}}]}}]}`,
		`{"choices":[{"delta":{"content":"answer ","reasoning":"more","tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{\"a\":1}"}},{"index":1,"function":{"arguments":"2}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"done","tool_calls":[{"index":0,"type":"function","function":{"name":"first"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16,"cost":0.01,"cost_details":{"upstream_inference_cost":0.008},"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":2},"completion_tokens_details":{"reasoning_tokens":3}}}`,
	}
	var assembler streamAssembler
	var deltas []Delta
	for _, event := range events {
		if err := assembler.push([]byte(event), func(delta Delta) {
			deltas = append(deltas, delta)
		}); err != nil {
			t.Fatal(err)
		}
	}
	completion := assembler.finish()

	wantDeltas := []Delta{
		{Kind: DeltaReasoning, Text: "think "},
		{Kind: DeltaText, Text: "answer "},
		{Kind: DeltaReasoning, Text: "more"},
		{Kind: DeltaText, Text: "done"},
	}
	if len(deltas) != len(wantDeltas) {
		t.Fatalf("deltas = %#v", deltas)
	}
	for index := range wantDeltas {
		if deltas[index] != wantDeltas[index] {
			t.Fatalf("delta %d = %#v, want %#v", index, deltas[index], wantDeltas[index])
		}
	}
	if completion.Text != "answer done" || completion.Reasoning != "think more" {
		t.Fatalf("completion text/reasoning = %q / %q", completion.Text, completion.Reasoning)
	}
	if completion.ID != "generation" ||
		completion.Model != "author/model" ||
		completion.Provider != "Example" ||
		completion.FinishReason != "tool_calls" {
		t.Fatalf("completion metadata = %#v", completion)
	}
	if len(completion.ToolCalls) != 2 {
		t.Fatalf("tool calls = %#v", completion.ToolCalls)
	}
	first, second := completion.ToolCalls[0], completion.ToolCalls[1]
	if first.ID != "call_1" ||
		first.Type != "function" ||
		first.Function.Name != "first" ||
		first.Function.Arguments != `{"a":1}` {
		t.Fatalf("first tool call = %#v", first)
	}
	if second.ID != "call_2" ||
		second.Function.Name != "second" ||
		second.Function.Arguments != `{"b":2}` {
		t.Fatalf("second tool call = %#v", second)
	}
	if completion.Usage == nil ||
		completion.Usage.CostDetails == nil ||
		completion.Usage.CostDetails.UpstreamInferenceCost != 0.008 ||
		completion.Usage.PromptTokensDetails == nil ||
		completion.Usage.PromptTokensDetails.CachedTokens != 7 ||
		completion.Usage.PromptTokensDetails.CacheWriteTokens != 2 ||
		completion.Usage.CompletionTokensDetails == nil ||
		completion.Usage.CompletionTokensDetails.ReasoningTokens != 3 {
		t.Fatalf("usage = %#v", completion.Usage)
	}
}

func TestAssemblerPreservesAllReasoningDetailKindsAndOrder(t *testing.T) {
	event := `{"choices":[{"delta":{"reasoning_details":[` +
		`{"type":"reasoning.summary","summary":"one"},` +
		`{"type":"reasoning.encrypted","id":"call_1","data":"opaque"},` +
		`{"type":"reasoning.text","text":"three","signature":"sig"}` +
		`],"tool_calls":[{"index":0,"id":"call_1"}]}}]}`
	var assembler streamAssembler
	if err := assembler.push([]byte(event), nil); err != nil {
		t.Fatal(err)
	}
	details := assembler.finish().ReasoningDetails
	if len(details) != 3 {
		t.Fatalf("details = %#v", details)
	}
	var kinds []string
	for _, detail := range details {
		var decoded struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(detail, &decoded); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, decoded.Type)
	}
	if strings.Join(kinds, ",") !=
		"reasoning.summary,reasoning.encrypted,reasoning.text" {
		t.Fatalf("detail kinds = %#v", kinds)
	}
}

func TestAssemblerMergesStreamedReasoningDetailFragmentsByIndex(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"first ","format":"provider-v1","index":0}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","id":"call_1","data":"opaque","index":1}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"second","signature":"signed","format":"provider-v1","index":0}],"tool_calls":[{"index":0,"id":"call_1"}]}}]}`,
	}
	var assembler streamAssembler
	for _, event := range events {
		if err := assembler.push([]byte(event), nil); err != nil {
			t.Fatal(err)
		}
	}
	details := assembler.finish().ReasoningDetails
	if len(details) != 2 {
		t.Fatalf("reasoning details = %s", details)
	}
	var textDetail struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Signature string `json:"signature"`
		Index     int    `json:"index"`
	}
	if err := json.Unmarshal(details[0], &textDetail); err != nil {
		t.Fatal(err)
	}
	if textDetail.Type != "reasoning.text" ||
		textDetail.Text != "first second" ||
		textDetail.Signature != "signed" ||
		textDetail.Index != 0 {
		t.Fatalf("merged text detail = %#v", textDetail)
	}
	var encryptedDetail struct {
		ID    string `json:"id"`
		Data  string `json:"data"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(details[1], &encryptedDetail); err != nil {
		t.Fatal(err)
	}
	if encryptedDetail.ID != "call_1" ||
		encryptedDetail.Data != "opaque" ||
		encryptedDetail.Index != 1 {
		t.Fatalf("encrypted detail = %#v", encryptedDetail)
	}
}

func TestAssemblerReturnsStreamError(t *testing.T) {
	var assembler streamAssembler
	err := assembler.push(
		[]byte(`{"error":{"code":502,"message":"provider unavailable"},"choices":[{"finish_reason":"error"}]}`),
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("error = %v", err)
	}
}
