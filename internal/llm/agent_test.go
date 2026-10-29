package llm

import (
	"context"
	"strings"
	"testing"
)

type scriptedClient struct {
	requests  []CompletionRequest
	responses []*CompletionResponse
}

func (c *scriptedClient) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &CompletionResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: "done"}, FinishReason: "stop"}}}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func (c *scriptedClient) Health(context.Context) error { return nil }
func (c *scriptedClient) Available() bool              { return true }

func TestRunAgentTerminalToolForcesFinalNoToolsAndSuppressesDuplicate(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		{
			Choices: []Choice{{
				Message: Message{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: FunctionCall{Name: "create_action", Arguments: `{"workspace_id":1,"action":{"name":"A"}}`}},
					{ID: "call_2", Type: "function", Function: FunctionCall{Name: "create_action", Arguments: `{
						"action": {"name":"A"},
						"workspace_id": 1
					}`}},
				}},
				FinishReason: "tool_calls",
			}},
		},
		{
			Choices: []Choice{{
				Message:      Message{Role: "assistant", Content: "Created action A."},
				FinishReason: "stop",
			}},
		},
	}}

	executions := 0
	result, err := RunAgent(
		context.Background(),
		client,
		AgentConfig{
			SystemPrompt:  "test",
			Tools:         []ToolDefinition{{Type: "function", Function: FunctionDef{Name: "create_action"}}},
			MaxIterations: 3,
			TerminalTools: map[string]bool{"create_action": true},
		},
		"create it",
		func(_ context.Context, name string, arguments string) (string, error) {
			executions++
			return `{"id":123,"name":"A"}`, nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if executions != 1 {
		t.Fatalf("expected duplicate terminal tool call to execute once, got %d", executions)
	}
	if result.Answer != "Created action A." {
		t.Fatalf("unexpected answer: %q", result.Answer)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected two tool call records, got %d", len(result.ToolCalls))
	}
	if !strings.Contains(result.ToolCalls[1].Result, "terminal side-effect already completed") {
		t.Fatalf("expected after-terminal suppression result, got %s", result.ToolCalls[1].Result)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.requests))
	}
	if len(client.requests[1].Tools) != 0 || client.requests[1].ToolChoice != nil {
		t.Fatalf("expected final request to have tools disabled, got tools=%d tool_choice=%v", len(client.requests[1].Tools), client.requests[1].ToolChoice)
	}
}

func TestRunAgentTerminalToolErrorDoesNotDisableTools(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		{
			Choices: []Choice{{
				Message: Message{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "call_1", Type: "function", Function: FunctionCall{Name: "create_action", Arguments: `{"workspace_id":1}`}},
				}},
				FinishReason: "tool_calls",
			}},
		},
		{
			Choices: []Choice{{
				Message:      Message{Role: "assistant", Content: "The action was invalid."},
				FinishReason: "stop",
			}},
		},
	}}

	_, err := RunAgent(
		context.Background(),
		client,
		AgentConfig{
			SystemPrompt:  "test",
			Tools:         []ToolDefinition{{Type: "function", Function: FunctionDef{Name: "create_action"}}},
			MaxIterations: 3,
			TerminalTools: map[string]bool{"create_action": true},
		},
		"create it",
		func(_ context.Context, name string, arguments string) (string, error) {
			return `{"error":"validation failed"}`, nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(client.requests))
	}
	if len(client.requests[1].Tools) == 0 || client.requests[1].ToolChoice == nil {
		t.Fatalf("expected tools to remain available after terminal tool error")
	}
}

func truncationResponse(content, finishReason string) *CompletionResponse {
	return &CompletionResponse{Choices: []Choice{{
		Message:      Message{Role: "assistant", Content: content},
		FinishReason: finishReason,
	}}}
}

func readCallResponse(id string) *CompletionResponse {
	return &CompletionResponse{Choices: []Choice{{
		Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID: id, Type: "function",
			Function: FunctionCall{Name: "get_item", Arguments: `{"item_id":1}`},
		}}},
		FinishReason: "tool_calls",
	}}}
}

func getItemTool() []ToolDefinition {
	return []ToolDefinition{{Type: "function", Function: FunctionDef{Name: "get_item"}}}
}

// A response cut off by the provider's output-token limit is truncated, not
// finished. Callers must see a distinct stop reason so they fail or flag it
// instead of presenting a partial answer as complete.
func TestRunAgentTruncatedResponseIsNotReportedAsDone(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		truncationResponse("Partial answer", "length"),
	}}

	result, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", MaxIterations: 3,
	}, "summarize", nil, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if result.StopReason != StopReasonMaxTokens {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonMaxTokens)
	}
	if result.Answer != "Partial answer" {
		t.Fatalf("answer = %q, want the partial content preserved", result.Answer)
	}
}

// A provider that returns neither tool calls nor text did not answer. Reporting
// that as `done` lets an empty turn pass for a completed one.
func TestRunAgentEmptyResponseIsNotReportedAsDone(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		truncationResponse("   ", "stop"),
	}}

	result, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", MaxIterations: 3,
	}, "summarize", nil, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if result.StopReason != StopReasonEmpty {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonEmpty)
	}
}

// A weak model that re-issues an identical read must not pay to run it again;
// the cached result is reused and the call is marked suppressed.
func TestRunAgentSuppressesDuplicateReadToolCalls(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		readCallResponse("call_1"),
		readCallResponse("call_2"),
		truncationResponse("Reviewed the item.", "stop"),
	}}

	executions := 0
	result, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", Tools: getItemTool(), MaxIterations: 5,
	}, "review", func(_ context.Context, _ string, _ string) (string, error) {
		executions++
		return `{"title":"Widget"}`, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if executions != 1 {
		t.Fatalf("duplicate read executed %d times, want 1", executions)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool call records = %d, want 2", len(result.ToolCalls))
	}
	if !strings.Contains(result.ToolCalls[1].Result, "duplicate read tool call suppressed") {
		t.Fatalf("second result = %s, want duplicate suppression", result.ToolCalls[1].Result)
	}
	if result.StopReason != StopReasonDone {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonDone)
	}
}

// A failed read is not cached: the model may legitimately retry it once the
// transient failure clears.
func TestRunAgentRetriesFailedReadToolCalls(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		readCallResponse("call_1"),
		readCallResponse("call_2"),
		truncationResponse("Recovered.", "stop"),
	}}

	executions := 0
	_, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", Tools: getItemTool(), MaxIterations: 5,
	}, "review", func(_ context.Context, _ string, _ string) (string, error) {
		executions++
		if executions == 1 {
			return `{"error":"temporary"}`, nil
		}
		return `{"title":"Widget"}`, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if executions != 2 {
		t.Fatalf("failed read executed %d times, want 2", executions)
	}
}

// Repeated identical reads that the cache already answered are no progress. The
// loop nudges once, then stops with a distinct reason instead of burning the
// rest of the iteration budget.
func TestRunAgentStopsAfterRepeatedNoProgress(t *testing.T) {
	client := &scriptedClient{responses: []*CompletionResponse{
		readCallResponse("call_1"),
		readCallResponse("call_2"),
		readCallResponse("call_3"),
		readCallResponse("call_4"),
	}}

	executions := 0
	result, err := RunAgent(context.Background(), client, AgentConfig{
		SystemPrompt: "test", Tools: getItemTool(), MaxIterations: 6,
	}, "review", func(_ context.Context, _ string, _ string) (string, error) {
		executions++
		return `{"title":"Widget"}`, nil
	}, nil)
	if err != nil {
		t.Fatalf("RunAgent returned error: %v", err)
	}
	if result.StopReason != StopReasonNoProgress {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonNoProgress)
	}
	if executions != 1 {
		t.Fatalf("read executed %d times, want 1", executions)
	}
}
