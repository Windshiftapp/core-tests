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
