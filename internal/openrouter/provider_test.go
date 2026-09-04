package openrouter

import "testing"

func TestProviderResolutionDefaultsRequireParametersForTools(t *testing.T) {
	resolved := (*Provider)(nil).resolved(true)
	if resolved == nil ||
		resolved.RequireParameters == nil ||
		!*resolved.RequireParameters {
		t.Fatalf("resolved provider = %#v", resolved)
	}
	if resolved := (*Provider)(nil).resolved(false); resolved != nil {
		t.Fatalf("plain request provider = %#v", resolved)
	}
}

func TestProviderResolutionNormalizesExplicitFalseToOmitted(t *testing.T) {
	disabled := false
	resolved := (&Provider{
		Sort:              "price",
		RequireParameters: &disabled,
	}).resolved(true)
	if resolved == nil || resolved.Sort != "price" {
		t.Fatalf("resolved provider = %#v", resolved)
	}
	if resolved.RequireParameters != nil {
		t.Fatalf("require_parameters = %#v", resolved.RequireParameters)
	}

	if resolved := (&Provider{RequireParameters: &disabled}).resolved(true); resolved != nil {
		t.Fatalf("empty normalized provider = %#v", resolved)
	}
}
