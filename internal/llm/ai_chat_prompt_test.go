package llm

import (
	"strings"
	"testing"
)

func TestAIChatPromptUsesItemKeysVerbatim(t *testing.T) {
	prompt := NewPromptStore("").Get(PromptAIChat)

	for _, want := range []string{
		"always use its `key` field verbatim",
		"never construct a reference from `id`",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("AI chat prompt missing %q", want)
		}
	}
}
