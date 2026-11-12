//go:build test

package llm

import (
	"context"
	"fmt"
	"testing"
)

// WI-1802: an HTTP read that fails transiently must not be cached. The retry
// may succeed, and suppressing it as a duplicate would stall the loop as
// no_progress without the provider ever receiving a retry.
func TestRunAgentRetriesTransientHTTPReadFailures(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			client := &scriptedClient{responses: []*CompletionResponse{
				readCallResponse("call_1"),
				readCallResponse("call_2"),
				truncationResponse("Recovered.", "stop"),
			}}

			executions := 0
			result, err := RunAgent(context.Background(), client, AgentConfig{
				SystemPrompt: "test", Tools: getItemTool(), MaxIterations: 5,
			}, "review", func(_ context.Context, _ string, _ string) (string, error) {
				executions++
				if executions == 1 {
					return fmt.Sprintf(`{"body":"busy","status_code":%d}`, status), nil
				}
				return `{"title":"Widget"}`, nil
			}, nil)
			if err != nil {
				t.Fatalf("RunAgent returned error: %v", err)
			}
			if executions != 2 {
				t.Fatalf("transient HTTP read executed %d times, want 2 (the retry must not be suppressed)", executions)
			}
			if result.StopReason != StopReasonDone {
				t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonDone)
			}
		})
	}
}

// A successful HTTP read is still cached: an identical repeat is suppressed.
func TestRunAgentCachesSuccessfulHTTPReads(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		readCallResponse("call_1"),
		readCallResponse("call_2"),
		truncationResponse("Reviewed.", "stop"),
	}}

	executions := 0
	result, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", Tools: getItemTool(), MaxIterations: 5,
	}, "review", func(_ context.Context, _ string, _ string) (string, error) {
		executions++
		return `{"body":"ok","status_code":200}`, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if executions != 1 {
		t.Fatalf("successful HTTP read executed %d times, want 1 (the duplicate must be suppressed)", executions)
	}
	if result.StopReason != StopReasonDone {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonDone)
	}
}
