package catalog_test

import (
	"testing"

	"ox/internal/catalog"
)

func TestLookupMatchesTheProviderAndTheModelID(t *testing.T) {
	cat := catalog.Catalog{
		{Provider: catalog.OpenRouter, ID: "openai/gpt-x:free"},
		{Provider: catalog.OpenRouter, ID: "gpt-x"},
		{Provider: catalog.OpenAI, ID: "gpt-x"},
	}
	for i, model := range cat {
		if got := cat.Lookup(model.QualifiedID()); got != model {
			t.Errorf("lookup %s = %v", model.QualifiedID(), got)
		}
		if i == 0 && model.QualifiedID() != "openrouter:openai/gpt-x:free" {
			t.Errorf("qualified ID = %s", model.QualifiedID())
		}
	}
	for _, qualified := range []string{"gpt-x", "anthropic:gpt-x", "openai:", "openai:gpt-y"} {
		if got := cat.Lookup(qualified); got != nil {
			t.Errorf("lookup %s = %v", qualified, got)
		}
	}
}
