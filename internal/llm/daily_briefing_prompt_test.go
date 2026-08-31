package llm

import (
	"strings"
	"testing"
)

func TestDailyBriefingPromptPrioritizesActionableOpenWork(t *testing.T) {
	prompt := NewPromptStore("").Get(PromptDailyBriefing)

	required := []string{
		"under 250 words",
		"## Today's Focus",
		"currently open",
		"## Deadlines & Risks",
		"at most 5 concise",
		"Do not use a table",
		"## Since Yesterday",
		"Always include this section",
		"If nothing material happened",
		"Do not enumerate completed items",
		"at most 3 bullets",
	}
	for _, text := range required {
		if !strings.Contains(prompt, text) {
			t.Errorf("daily briefing prompt missing %q", text)
		}
	}

	if strings.Contains(prompt, "## Activity Recap") {
		t.Fatal("daily briefing prompt still leads with an activity recap")
	}
	if strings.Contains(prompt, "This section is optional") {
		t.Fatal("daily briefing prompt allows the recap to be omitted")
	}
	if strings.Contains(prompt, "Use a compact table") {
		t.Fatal("daily briefing prompt still requests a deadlines table")
	}
	if strings.Index(prompt, "## Today's Focus") > strings.Index(prompt, "## Since Yesterday") {
		t.Fatal("daily briefing prompt should put today's open work before recent activity")
	}
}
