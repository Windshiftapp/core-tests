package services

import (
	"testing"

	"windshift/internal/llm"
)

// TestApplyLLMModelEnv_InjectsVision verifies the container receives only
// packing/capability limits; provider model/protocol selection stays server-side.
func TestApplyLLMModelEnv_InjectsVision(t *testing.T) {
	cases := []struct {
		name            string
		cfg             *llm.ConnectionRuntimeConfig
		wantVision      string
		wantVisionUnset bool
	}{
		{
			name:       "vision model",
			cfg:        &llm.ConnectionRuntimeConfig{Model: "gpt-4o", Protocol: llm.APIContractResponses, SupportsVision: true, ContextWindow: 128000, MaxOutputTokens: 4096},
			wantVision: "true",
		},
		{
			name:       "text-only model",
			cfg:        &llm.ConnectionRuntimeConfig{Model: "qwen3-coder", Protocol: llm.APIContractChatCompletions, SupportsVision: false, ContextWindow: 262144, MaxOutputTokens: 65536},
			wantVision: "false",
		},
		{
			name:            "nil cfg sets nothing",
			cfg:             nil,
			wantVisionUnset: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			applyLLMModelEnv(env, tc.cfg)
			if tc.wantVisionUnset {
				if _, ok := env["LLM_SUPPORTS_VISION"]; ok {
					t.Error("nil cfg should not set LLM_SUPPORTS_VISION")
				}
				return
			}
			if env["LLM_MODEL"] != "" || env["LLM_PROTOCOL"] != "" {
				t.Fatalf("provider routing leaked into container env: %#v", env)
			}
			if env["LLM_SUPPORTS_VISION"] != tc.wantVision {
				t.Errorf("LLM_SUPPORTS_VISION = %q, want %q", env["LLM_SUPPORTS_VISION"], tc.wantVision)
			}
			if !tc.wantVisionUnset && (env["LLM_CONTEXT_WINDOW"] == "" || env["LLM_MAX_TOKENS"] == "") {
				t.Errorf("resolved limits missing: %#v", env)
			}
		})
	}
}
