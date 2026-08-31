//go:build test

package scheduler

import "testing"

func TestDailyBriefingCompletionRequestAllowsFiveThousandOutputTokens(t *testing.T) {
	request := dailyBriefingCompletionRequest("system prompt", "user prompt")

	if request.MaxTokens != 5000 {
		t.Fatalf("expected daily briefing output budget of 5000 tokens, got %d", request.MaxTokens)
	}
}
