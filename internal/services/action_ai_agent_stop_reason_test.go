//go:build test

package services

import (
	"context"
	"encoding/json"
	"testing"

	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/testutils"
)

// stubLLMClient lets tests script what the LLM returns each iteration.
type stubLLMClient struct {
	responses []llm.CompletionResponse
	calls     int
}

func (s *stubLLMClient) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if s.calls >= len(s.responses) {
		// Repeat the last response forever.
		resp := s.responses[len(s.responses)-1]
		s.calls++
		return &resp, nil
	}
	resp := s.responses[s.calls]
	s.calls++
	return &resp, nil
}

func (s *stubLLMClient) Health(_ context.Context) error { return nil }
func (s *stubLLMClient) Available() bool                { return true }

type stubLLMResolver struct{ client llm.Client }

func (r *stubLLMResolver) Resolve(int) (llm.Client, error) { return r.client, nil }

func toolCallResponse(toolName string) llm.CompletionResponse {
	return llm.CompletionResponse{
		Choices: []llm.Choice{{
			FinishReason: "tool_calls",
			Message: llm.Message{
				ToolCalls: []llm.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      toolName,
						Arguments: `{"method": "GET", "url": "http://127.0.0.1:1/ping"}`,
					},
				}},
			},
		}},
	}
}

// Regression (bughunt, actions): an ai_agent node that exhausts its
// max_steps budget used to complete successfully, storing the loop's
// boilerplate "I wasn't able to complete the task..." answer in the output
// field. Downstream add_comment/notify nodes then posted that boilerplate as
// if it were a real answer. A non-done stop reason must fail the step
// instead, and must not populate the output variable.
func TestAIAgentStepFailsWhenBudgetExhausted(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	llmCapID := testutils.InsertID(t, db, `
		INSERT INTO action_capabilities (name, capability_type, config, is_enabled, applies_to_all_workspaces)
		VALUES ('llm', 'llm_connection', '{"connection_id": 1}', true, true)
	`)
	capabilityID := testutils.InsertID(t, db, `
		INSERT INTO action_capabilities (name, capability_type, config, is_enabled, applies_to_all_workspaces)
		VALUES ('agent http', 'http_client', '{"allowed_url_patterns": ["*"]}', true, true)
	`)

	// Every iteration answers with a tool call, so the loop never reaches a
	// final answer and exhausts the budget. The tool target is a closed local
	// port so the executor fails fast without real network access — tool
	// errors keep the loop alive until the budget is gone.
	client := &stubLLMClient{responses: []llm.CompletionResponse{toolCallResponse("http_request_" + aiTestID(capabilityID))}}
	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.SetLLMConnectionManager(&stubLLMResolver{client: client})

	node := &models.ActionNode{
		NodeType: models.ActionNodeAIAgent,
		NodeConfig: `{
			"capability_id": ` + aiTestID(llmCapID) + `,
			"prompt": "do a read-only probe",
			"input_fields": ["item_id"],
			"tools": ["` + aiTestID(capabilityID) + `"],
			"max_steps": 2,
			"output_field": "agent_answer"
		}`,
	}
	ctx := &models.ExecutionContext{
		Context:          context.Background(),
		Event:            &models.ActionEvent{ItemID: 1, WorkspaceID: data.WorkspaceID},
		EffectiveActorID: data.UserID,
		Variables:        map[string]any{"item_id": 1},
	}
	step := &models.StepResult{NodeID: node.ID, NodeType: node.NodeType}

	err := service.executeAIAgent(node, ctx, step)
	if err == nil {
		t.Fatalf("ai_agent completed with stop_reason=%q; want budget-exhaustion failure", step.Output["stop_reason"])
	}
	if _, populated := ctx.Variables["agent_answer"]; populated {
		t.Fatal("output field was populated despite budget exhaustion")
	}
	if step.Output["stop_reason"] != string(llm.StopReasonMaxIterations) {
		t.Fatalf("step stop_reason = %v, want %q", step.Output["stop_reason"], llm.StopReasonMaxIterations)
	}
}

// A clean run (no tool calls, direct answer) still completes and populates
// the output field.
func TestAIAgentStepCompletesOnFinalAnswer(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	llmCapID := testutils.InsertID(t, db, `
		INSERT INTO action_capabilities (name, capability_type, config, is_enabled, applies_to_all_workspaces)
		VALUES ('llm', 'llm_connection', '{"connection_id": 1}', true, true)
	`)
	client := &stubLLMClient{responses: []llm.CompletionResponse{{
		Choices: []llm.Choice{{
			FinishReason: "stop",
			Message:      llm.Message{Content: "all done"},
		}},
	}}}
	service := NewActionService(db, DefaultActionServiceConfig(), nil)
	t.Cleanup(service.Stop)
	service.SetLLMConnectionManager(&stubLLMResolver{client: client})

	node := &models.ActionNode{
		NodeType: models.ActionNodeAIAgent,
		NodeConfig: `{
			"capability_id": ` + aiTestID(llmCapID) + `,
			"prompt": "summarize",
			"input_fields": ["item_id"],
			"output_field": "agent_answer"
		}`,
	}
	ctx := &models.ExecutionContext{
		Context:          context.Background(),
		Event:            &models.ActionEvent{ItemID: 1, WorkspaceID: data.WorkspaceID},
		EffectiveActorID: data.UserID,
		Variables:        map[string]any{"item_id": 1},
	}
	step := &models.StepResult{NodeID: node.ID, NodeType: node.NodeType}

	if err := service.executeAIAgent(node, ctx, step); err != nil {
		t.Fatalf("executeAIAgent: %v", err)
	}
	if got := ctx.Variables["agent_answer"]; got != "all done" {
		t.Fatalf("output field = %v, want %q", got, "all done")
	}
	if step.Output["stop_reason"] != string(llm.StopReasonDone) {
		t.Fatalf("step stop_reason = %v, want %q", step.Output["stop_reason"], llm.StopReasonDone)
	}
}

func aiTestID(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
