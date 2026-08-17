package openrouter

import (
	"strings"
	"testing"
)

func TestAssemblerSeparatesTextAndReasoningAndKeepsTrailingUsage(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"reasoning":"think "}}]}`,
		`{"choices":[{"delta":{"content":"answer ","reasoning":"more"}}]}`,
		`{"choices":[{"delta":{"content":"","reasoning":""}}]}`,
		`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16}}`,
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
		t.Fatalf("text/reasoning = %q / %q", completion.Text, completion.Reasoning)
	}
	if completion.FinishReason != "stop" {
		t.Fatalf("finish reason = %q", completion.FinishReason)
	}
	if completion.Usage == nil ||
		completion.Usage.PromptTokens != 10 ||
		completion.Usage.CompletionTokens != 6 ||
		completion.Usage.TotalTokens != 16 {
		t.Fatalf("usage = %#v", completion.Usage)
	}
}

func TestAssemblerReportsAChunkWithoutAChoice(t *testing.T) {
	var assembler streamAssembler
	if err := assembler.push([]byte(`{"choices":[],"usage":{"total_tokens":1}}`), nil); err != nil {
		t.Fatal(err)
	}
	if assembler.sawChoice {
		t.Fatal("sawChoice = true, want false")
	}
	if err := assembler.push([]byte(`{"choices":[{"delta":{}}]}`), nil); err != nil {
		t.Fatal(err)
	}
	if !assembler.sawChoice {
		t.Fatal("sawChoice = false, want true")
	}
}

func TestAssemblerReturnsStreamError(t *testing.T) {
	for _, test := range []struct {
		name  string
		event string
		want  string
	}{
		{
			name:  "with code",
			event: `{"error":{"code":502,"message":"provider unavailable"},"choices":[]}`,
			want:  "OpenRouter streaming error 502: provider unavailable",
		},
		{
			name:  "without code",
			event: `{"error":{"message":"provider unavailable"}}`,
			want:  "OpenRouter streaming error: provider unavailable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var assembler streamAssembler
			err := assembler.push([]byte(test.event), nil)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestAssemblerReportsAMalformedChunk(t *testing.T) {
	var assembler streamAssembler
	err := assembler.push([]byte(`{`), nil)
	if err == nil || !strings.Contains(err.Error(), "decode OpenRouter stream event") {
		t.Fatalf("error = %v", err)
	}
}
