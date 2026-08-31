package llm

import (
	"encoding/json"
	"testing"
	"time"
)

// TestResolveModelVision_Precedence verifies the cache wins over the curated
// map: a cache row marking an otherwise-unrecognized id vision-capable is
// honored, and the curated map still covers ids absent from the cache.
func TestResolveModelVision_Precedence(t *testing.T) {
	db := newRefresherTestDB(t)
	cache := NewModelCache(db)
	// Cache says a custom id is vision-capable; curated map would say false.
	if err := cache.SaveSuccess("openrouter", []ModelInfo{
		{ID: "custom/unknown-multimodal", SupportsVision: true},
	}, time.Now()); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	m := &ConnectionManager{}
	m.SetModelCache(cache)

	if !m.resolveModelVision("openrouter", "custom/unknown-multimodal") {
		t.Error("cache vision flag should win for a custom id")
	}
	// Not in cache, not a static seed for this provider → curated map applies.
	if !m.resolveModelVision("openrouter", "anthropic/claude-3.5-sonnet") {
		t.Error("curated map should mark claude-3.5 vision-capable")
	}
	if m.resolveModelVision("openrouter", "vendor/plain-text-model") {
		t.Error("unrecognized text-only id should resolve vision-off")
	}
}

func TestProviderConfigVisionMode(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty defaults auto", "", "auto"},
		{"absent key defaults auto", `{"temperature":0.2}`, "auto"},
		{"explicit on", `{"vision_mode":"on"}`, "on"},
		{"explicit off", `{"vision_mode":"off"}`, "off"},
		{"explicit auto", `{"vision_mode":"auto"}`, "auto"},
		{"case-insensitive", `{"vision_mode":"On"}`, "on"},
		{"invalid value falls back auto", `{"vision_mode":"maybe"}`, "auto"},
		{"non-string falls back auto", `{"vision_mode":true}`, "auto"},
		{"garbage blob falls back auto", `not json`, "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderConfigVisionMode(tc.raw); got != tc.want {
				t.Errorf("ProviderConfigVisionMode(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestValidateProviderConfig_VisionMode(t *testing.T) {
	if err := ValidateProviderConfig(`{"vision_mode":"on"}`); err != nil {
		t.Errorf("valid vision_mode rejected: %v", err)
	}
	if err := ValidateProviderConfig(`{"vision_mode":"sometimes"}`); err == nil {
		t.Error("invalid vision_mode value should be rejected")
	}
	if err := ValidateProviderConfig(`{"vision_mode":5}`); err == nil {
		t.Error("non-string vision_mode should be rejected")
	}
}

// TestMergeProviderConfig_StripsReservedKeys is the load-bearing guard: the
// windshift-private settings must never reach the provider request body,
// while ordinary keys still merge.
func TestMergeProviderConfig_StripsReservedKeys(t *testing.T) {
	body := map[string]interface{}{"model": "gpt-4o"}
	if err := MergeProviderConfig(body, `{"vision_mode":"on","api_contract":"responses","temperature":0.3}`); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, leaked := body["vision_mode"]; leaked {
		t.Error("vision_mode must not be merged into the provider body")
	}
	if _, leaked := body["api_contract"]; leaked {
		t.Error("api_contract must not be merged into the provider body")
	}
	if body["temperature"] != 0.3 {
		t.Errorf("ordinary key should merge, got %v", body["temperature"])
	}
}

func TestMergeProviderConfigJSON_StripsReservedKeys(t *testing.T) {
	merged, err := MergeProviderConfigJSON([]byte(`{"model":"gpt-4o"}`), `{"vision_mode":"off","api_contract":"chat_completions","top_p":0.9}`)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if _, leaked := got["vision_mode"]; leaked {
		t.Error("vision_mode must not be forwarded to the provider")
	}
	if _, leaked := got["api_contract"]; leaked {
		t.Error("api_contract must not be forwarded to the provider")
	}
	if _, ok := got["top_p"]; !ok {
		t.Error("ordinary key top_p should be forwarded")
	}
}

func TestEffectiveVision(t *testing.T) {
	cases := []struct {
		mode     string
		modelCap bool
		want     bool
	}{
		{"on", false, true},  // override forces on even for a text model
		{"off", true, false}, // override forces off even for a vision model
		{"auto", true, true}, // auto defers to capability
		{"auto", false, false},
		{"", true, true},      // empty == auto
		{"bogus", true, true}, // unrecognized == auto
	}
	for _, tc := range cases {
		if got := EffectiveVision(tc.mode, tc.modelCap); got != tc.want {
			t.Errorf("EffectiveVision(%q, %v) = %v, want %v", tc.mode, tc.modelCap, got, tc.want)
		}
	}
}
