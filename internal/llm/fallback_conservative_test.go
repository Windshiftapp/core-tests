//go:build test

package llm

import "testing"

// WI-1796: an unknown model's output cap must be conservative. A model that
// supports fewer output tokens than the fallback rejects the request outright,
// which is worse than truncating an unusually long answer. The fallback must
// therefore not exceed the smallest output limit any catalogued model
// advertises.
func TestUnknownModelOutputFallbackIsConservative(t *testing.T) {
	LoadDefaultProviders()

	smallest := 0
	for _, p := range GetProviders() {
		for _, m := range p.Models {
			if m.MaxTokens > 0 && (smallest == 0 || m.MaxTokens < smallest) {
				smallest = m.MaxTokens
			}
		}
	}
	if smallest == 0 {
		t.Fatal("catalog advertises no output limits")
	}
	if FallbackMaxOutputTokens > smallest {
		t.Fatalf("fallback output cap %d exceeds the smallest catalog limit %d; an unknown model supporting fewer tokens would reject the request",
			FallbackMaxOutputTokens, smallest)
	}
}
