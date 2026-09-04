package openrouter

import (
	"os"
	"testing"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("OX_LIVE_TESTS") != "1" {
		t.Skip("set OX_LIVE_TESTS=1 to run live OpenRouter tests")
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	return &Client{
		APIKey:            func() string { return apiKey },
		cachePathOverride: t.TempDir() + "/models.json",
	}
}

func TestLiveStreamingCompletion(t *testing.T) {
	client := liveClient(t)
	model := os.Getenv("OX_LIVE_MODEL")
	if model == "" {
		model = "openai/gpt-4.1-mini"
	}
	completion, err := client.Stream(t.Context(), Request{
		Model:        model,
		SessionID:    "ox-live-test",
		CacheControl: &CacheControl{Type: "ephemeral"},
		Messages: []Message{{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: "text", Text: "Reply with only: ox"}},
		}},
		MaxTokens: intPointer(16),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text == "" {
		t.Fatalf("empty completion: %#v", completion)
	}
	if completion.Usage == nil || completion.Usage.TotalTokens == 0 {
		t.Fatalf("missing usage: %#v", completion.Usage)
	}
}

func TestLiveCatalog(t *testing.T) {
	catalog, err := liveClient(t).Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models()) == 0 {
		t.Fatal("OpenRouter returned an empty model catalog")
	}
}

func intPointer(value int) *int {
	return &value
}
